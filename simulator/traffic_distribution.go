package simulator

import (
	"math"
	"math/rand"
)

// TrafficDistribution interface for generating write events
// Generates both write size and time until next write
type TrafficDistribution interface {
	// NextWriteSizeMB returns the size of the next write in MB
	NextWriteSizeMB() float64
	// NextIntervalSeconds returns the time until the next write in seconds
	NextIntervalSeconds() float64
}

// ConstantTrafficDistribution generates writes at a constant rate
type ConstantTrafficDistribution struct {
	writeRateMBps float64
	writeSizeMB   float64
}

// NewConstantTrafficDistribution creates a constant rate traffic distribution
func NewConstantTrafficDistribution(writeRateMBps float64) TrafficDistribution {
	return &ConstantTrafficDistribution{
		writeRateMBps: writeRateMBps,
		writeSizeMB:   1.0, // Fixed 1MB writes
	}
}

func (d *ConstantTrafficDistribution) NextWriteSizeMB() float64 {
	return d.writeSizeMB
}

func (d *ConstantTrafficDistribution) NextIntervalSeconds() float64 {
	if d.writeRateMBps <= 0 {
		return 0 // No writes if rate is 0
	}
	return d.writeSizeMB / d.writeRateMBps
}

// AdvancedTrafficDistribution implements ON/OFF lognormal model with spikes
type AdvancedTrafficDistribution struct {
	// Base regime parameters
	baseRateMBps    float64 // B: baseline rate in MB/s
	burstMultiplier float64 // M: multiplier for burst regime
	lognormalSigma  float64 // σ: lognormal variance parameter

	// ON/OFF state machine
	isON            bool    // Current state (ON or OFF)
	onDurationLeft  float64 // Remaining time in ON state
	offDurationLeft float64 // Remaining time in OFF state
	onMeanSeconds   float64 // Mean ON duration
	offMeanSeconds  float64 // Mean OFF duration
	erlangK         int     // Erlang shape parameter for ON periods

	// Spike parameters
	spikeRatePerSec     float64 // Poisson rate for spike arrival
	spikeMeanDur        float64 // Mean spike duration
	spikeAmplitudeMean  float64 // Mean spike amplitude (log space)
	spikeAmplitudeSigma float64 // Spike amplitude variance (log space)
	activeSpikes        []spike // Currently active spikes

	// Capacity limits
	capacityLimitMB float64 // Capacity limit (0 = unlimited)
	queueMode       string  // "drop" or "queue"
	queueBacklog    float64 // Accumulated backlog in queue mode

	// Random number generator
	rng *rand.Rand

	// Time tracking for state machine
	lastUpdateTime float64 // Last virtual time when state was updated
}

type spike struct {
	amplitude    float64 // Current spike amplitude
	durationLeft float64 // Remaining duration
}

// AdvancedTrafficDistributionConfig holds configuration for advanced traffic distribution
type AdvancedTrafficDistributionConfig struct {
	BaseRateMBps        float64 // B: baseline rate in MB/s
	BurstMultiplier     float64 // M: multiplier for burst regime
	LognormalSigma      float64 // σ: lognormal variance parameter
	OnMeanSeconds       float64 // Mean ON duration
	OffMeanSeconds      float64 // Mean OFF duration
	ErlangK             int     // Erlang shape parameter for ON periods
	SpikeRatePerSec     float64 // Poisson rate for spike arrival
	SpikeMeanDur        float64 // Mean spike duration
	SpikeAmplitudeMean  float64 // Mean spike amplitude (log space)
	SpikeAmplitudeSigma float64 // Spike amplitude variance (log space)
	CapacityLimitMB     float64 // Capacity limit (0 = unlimited)
	QueueMode           string  // "drop" or "queue"
}

// NewAdvancedTrafficDistribution creates an advanced ON/OFF traffic distribution
func NewAdvancedTrafficDistribution(config AdvancedTrafficDistributionConfig, seed int64) TrafficDistribution {
	var rng *rand.Rand
	if seed == 0 {
		rng = rand.New(rand.NewSource(rand.Int63()))
	} else {
		rng = rand.New(rand.NewSource(seed))
	}

	// Start in OFF state
	offDuration := exponentialSample(rng, config.OffMeanSeconds)

	return &AdvancedTrafficDistribution{
		baseRateMBps:        config.BaseRateMBps,
		burstMultiplier:     config.BurstMultiplier,
		lognormalSigma:      config.LognormalSigma,
		isON:                false,
		onDurationLeft:      0,
		offDurationLeft:     offDuration,
		onMeanSeconds:       config.OnMeanSeconds,
		offMeanSeconds:      config.OffMeanSeconds,
		erlangK:             config.ErlangK,
		spikeRatePerSec:     config.SpikeRatePerSec,
		spikeMeanDur:        config.SpikeMeanDur,
		spikeAmplitudeMean:  config.SpikeAmplitudeMean,
		spikeAmplitudeSigma: config.SpikeAmplitudeSigma,
		capacityLimitMB:     config.CapacityLimitMB,
		queueMode:           config.QueueMode,
		queueBacklog:        0,
		activeSpikes:        make([]spike, 0),
		rng:                 rng,
		lastUpdateTime:      0.0, // Will be set on first call
	}
}

// NextWriteSizeMB generates the next write size using the current regime
func (d *AdvancedTrafficDistribution) NextWriteSizeMB() float64 {
	// State machine is updated via UpdateTime() call from simulator

	// Determine base rate for current state
	var baseRate float64
	if d.isON {
		baseRate = d.baseRateMBps * d.burstMultiplier
	} else {
		baseRate = d.baseRateMBps
	}

	// Generate lognormal sample
	// Lognormal: Y = exp(N(0, σ²)) where N is normal
	// For mean ≈ B, we use: Y = B * exp(N(0, σ²) - σ²/2) to center at B
	normalSample := d.rng.NormFloat64() * d.lognormalSigma
	rateSample := baseRate * math.Exp(normalSample-(d.lognormalSigma*d.lognormalSigma)/2.0)

	// Add active spikes
	spikeAmplitude := 0.0
	for _, s := range d.activeSpikes {
		spikeAmplitude += s.amplitude
	}

	totalRate := rateSample + spikeAmplitude

	// Generate write size (fixed 1MB per write, but rate varies)
	// For simplicity, we keep write size constant but vary interval
	writeSizeMB := 1.0

	// Apply capacity limits
	if d.capacityLimitMB > 0 {
		if totalRate > d.capacityLimitMB {
			if d.queueMode == "drop" {
				// Truncate at capacity
				totalRate = d.capacityLimitMB
			} else {
				// Queue mode: accumulate excess
				excess := totalRate - d.capacityLimitMB
				d.queueBacklog += excess
				// Backlog will be consumed in future intervals
			}
		}
	}

	return writeSizeMB
}

// NextIntervalSeconds returns time until next write
func (d *AdvancedTrafficDistribution) NextIntervalSeconds() float64 {
	// State machine is updated via UpdateTime() call from simulator

	// Determine base rate for current state
	var baseRate float64
	if d.isON {
		baseRate = d.baseRateMBps * d.burstMultiplier
	} else {
		baseRate = d.baseRateMBps
	}

	// Generate lognormal sample for base rate
	normalSample := d.rng.NormFloat64() * d.lognormalSigma
	rateSample := baseRate * math.Exp(normalSample-(d.lognormalSigma*d.lognormalSigma)/2.0)

	// Add active spikes
	spikeAmplitude := 0.0
	for _, s := range d.activeSpikes {
		spikeAmplitude += s.amplitude
	}

	totalRate := rateSample + spikeAmplitude

	// Handle queue backlog
	if d.queueBacklog > 0 && d.queueMode == "queue" {
		totalRate += d.queueBacklog
		// Consume backlog over time (simplified: immediately consume)
		d.queueBacklog = 0
	}

	if totalRate <= 0 {
		return 0
	}

	// Fixed write size of 1MB
	writeSizeMB := 1.0
	interval := writeSizeMB / totalRate

	// Apply capacity limits
	if d.capacityLimitMB > 0 && totalRate > d.capacityLimitMB {
		if d.queueMode == "drop" {
			// Truncate at capacity
			interval = writeSizeMB / d.capacityLimitMB
		}
		// Queue mode: interval already accounts for backlog
	}

	return interval
}

// GetCurrentRateMBps returns the current effective write rate (for display)
// This is the rate that would be used for the next write, including ON/OFF state and spikes
func (d *AdvancedTrafficDistribution) GetCurrentRateMBps() float64 {
	// Determine base rate for current state
	var baseRate float64
	if d.isON {
		baseRate = d.baseRateMBps * d.burstMultiplier
	} else {
		baseRate = d.baseRateMBps
	}

	// Generate lognormal sample for base rate (for consistency, use same logic as NextIntervalSeconds)
	normalSample := d.rng.NormFloat64() * d.lognormalSigma
	rateSample := baseRate * math.Exp(normalSample-(d.lognormalSigma*d.lognormalSigma)/2.0)

	// Add active spikes
	spikeAmplitude := 0.0
	for _, s := range d.activeSpikes {
		spikeAmplitude += s.amplitude
	}

	totalRate := rateSample + spikeAmplitude

	// Handle queue backlog
	if d.queueBacklog > 0 && d.queueMode == "queue" {
		totalRate += d.queueBacklog
	}

	// Apply capacity limits
	if d.capacityLimitMB > 0 && totalRate > d.capacityLimitMB {
		if d.queueMode == "drop" {
			totalRate = d.capacityLimitMB
		}
	}

	return totalRate
}

// UpdateTime updates the distribution with current virtual time
// This allows the state machine to track actual elapsed time
func (d *AdvancedTrafficDistribution) UpdateTime(currentTime float64) {
	if d.lastUpdateTime == 0.0 {
		// First call - initialize
		d.lastUpdateTime = currentTime
		return
	}
	d.updateState(currentTime - d.lastUpdateTime)
	d.lastUpdateTime = currentTime
}

// updateState updates the ON/OFF state machine and spike states
// deltaTime is the actual elapsed time since last update
func (d *AdvancedTrafficDistribution) updateState(deltaTime float64) {
	if deltaTime <= 0 {
		return // No time elapsed
	}

	// Cap deltaTime to prevent huge jumps (e.g., if simulation was paused)
	if deltaTime > 1.0 {
		deltaTime = 1.0 // Cap at 1 second per update
	}

	// Check for new spike arrivals (Poisson process)
	if d.spikeRatePerSec > 0 {
		// Poisson probability: P(k arrivals in time t) where rate = λ
		// For small intervals, P(≥1) ≈ λ * t
		probPerUpdate := d.spikeRatePerSec * deltaTime
		if probPerUpdate > 1.0 {
			probPerUpdate = 1.0
		}
		if d.rng.Float64() < probPerUpdate {
			// Generate new spike
			spikeDur := exponentialSample(d.rng, d.spikeMeanDur)
			// Generate lognormal amplitude
			normalSample := d.rng.NormFloat64() * d.spikeAmplitudeSigma
			spikeAmp := math.Exp(d.spikeAmplitudeMean + normalSample - (d.spikeAmplitudeSigma*d.spikeAmplitudeSigma)/2.0)
			d.activeSpikes = append(d.activeSpikes, spike{
				amplitude:    spikeAmp,
				durationLeft: spikeDur,
			})
		}
	}

	// Update spike durations
	newSpikes := make([]spike, 0)
	for _, s := range d.activeSpikes {
		s.durationLeft -= deltaTime
		if s.durationLeft > 0 {
			newSpikes = append(newSpikes, s)
		}
	}
	d.activeSpikes = newSpikes

	// Update ON/OFF state
	if d.isON {
		d.onDurationLeft -= deltaTime
		if d.onDurationLeft <= 0 {
			// Transition to OFF
			d.isON = false
			d.offDurationLeft = exponentialSample(d.rng, d.offMeanSeconds)
		}
	} else {
		d.offDurationLeft -= deltaTime
		if d.offDurationLeft <= 0 {
			// Transition to ON
			d.isON = true
			d.onDurationLeft = erlangSample(d.rng, d.erlangK, d.onMeanSeconds)
		}
	}
}

// exponentialSample generates an exponential random variable
func exponentialSample(rng *rand.Rand, mean float64) float64 {
	if mean <= 0 {
		return 0
	}
	u := rng.Float64()
	if u == 0 {
		u = 1e-10 // Avoid log(0)
	}
	return -mean * math.Log(u)
}

// erlangSample generates an Erlang(k, λ) random variable
// Mean = k/λ, so λ = k/mean
func erlangSample(rng *rand.Rand, k int, mean float64) float64 {
	if mean <= 0 || k <= 0 {
		return 0
	}
	lambda := float64(k) / mean
	// Erlang(k, λ) is sum of k independent exponentials with rate λ
	sum := 0.0
	for i := 0; i < k; i++ {
		sum += exponentialSample(rng, 1.0/lambda)
	}
	return sum
}

// NewTrafficDistribution creates a traffic distribution from config
func NewTrafficDistribution(config TrafficDistributionConfig, seed int64) TrafficDistribution {
	switch config.Model {
	case TrafficModelAdvancedONOFF:
		return NewAdvancedTrafficDistribution(
			AdvancedTrafficDistributionConfig{
				BaseRateMBps:        config.BaseRateMBps,
				BurstMultiplier:     config.BurstMultiplier,
				LognormalSigma:      config.LognormalSigma,
				OnMeanSeconds:       config.OnMeanSeconds,
				OffMeanSeconds:      config.OffMeanSeconds,
				ErlangK:             config.ErlangK,
				SpikeRatePerSec:     config.SpikeRatePerSec,
				SpikeMeanDur:        config.SpikeMeanDur,
				SpikeAmplitudeMean:  config.SpikeAmplitudeMean,
				SpikeAmplitudeSigma: config.SpikeAmplitudeSigma,
				CapacityLimitMB:     config.CapacityLimitMB,
				QueueMode:           config.QueueMode,
			},
			seed,
		)
	default: // TrafficModelConstant
		return NewConstantTrafficDistribution(config.WriteRateMBps)
	}
}
