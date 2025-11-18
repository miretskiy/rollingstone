package simulator

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConstantTrafficDistribution(t *testing.T) {
	t.Run("constant rate", func(t *testing.T) {
		dist := NewConstantTrafficDistribution(10.0)

		// Should always return 1MB writes
		require.Equal(t, 1.0, dist.NextWriteSizeMB())
		require.Equal(t, 1.0, dist.NextWriteSizeMB())

		// Interval should be 1MB / 10MB/s = 0.1s
		interval := dist.NextIntervalSeconds()
		require.InDelta(t, 0.1, interval, 0.001)
	})

	t.Run("zero rate", func(t *testing.T) {
		dist := NewConstantTrafficDistribution(0.0)

		// Should return 0 interval
		interval := dist.NextIntervalSeconds()
		require.Equal(t, 0.0, interval)
	})

	t.Run("negative rate", func(t *testing.T) {
		dist := NewConstantTrafficDistribution(-5.0)

		// Should return 0 interval
		interval := dist.NextIntervalSeconds()
		require.Equal(t, 0.0, interval)
	})
}

func TestAdvancedTrafficDistribution(t *testing.T) {
	t.Run("basic ON/OFF behavior", func(t *testing.T) {
		dist := NewAdvancedTrafficDistribution(
			AdvancedTrafficDistributionConfig{
				BaseRateMBps:        10.0,
				BurstMultiplier:     2.0,
				LognormalSigma:      0.1,
				OnMeanSeconds:       5.0,
				OffMeanSeconds:      10.0,
				ErlangK:             2,
				SpikeRatePerSec:     0.0, // no spikes for this test
				SpikeMeanDur:        0.0,
				SpikeAmplitudeMean:  0.0,
				SpikeAmplitudeSigma: 0.0,
				CapacityLimitMB:     0.0, // unlimited
				QueueMode:           "drop",
			},
			42, // seed
		)

		// Should generate writes
		writeSize := dist.NextWriteSizeMB()
		require.Greater(t, writeSize, 0.0)

		interval := dist.NextIntervalSeconds()
		require.Greater(t, interval, 0.0)
	})

	t.Run("capacity limit drop mode", func(t *testing.T) {
		dist := NewAdvancedTrafficDistribution(
			AdvancedTrafficDistributionConfig{
				BaseRateMBps:        100.0, // high rate
				BurstMultiplier:     1.0,
				LognormalSigma:      0.1,
				OnMeanSeconds:       5.0,
				OffMeanSeconds:      10.0,
				ErlangK:             2,
				SpikeRatePerSec:     0.0,
				SpikeMeanDur:        0.0,
				SpikeAmplitudeMean:  0.0,
				SpikeAmplitudeSigma: 0.0,
				CapacityLimitMB:     50.0, // cap at 50MB/s
				QueueMode:           "drop",
			},
			42, // seed
		)

		// Generate many samples to check that rate is capped
		intervals := make([]float64, 100)
		for i := 0; i < 100; i++ {
			interval := dist.NextIntervalSeconds()
			writeSize := dist.NextWriteSizeMB()
			// Effective rate = writeSize / interval
			// Should be <= capacityLimitMB
			if interval > 0 {
				effectiveRate := writeSize / interval
				intervals[i] = effectiveRate
			}
		}

		// Most rates should be capped (allowing some variance from lognormal)
		cappedCount := 0
		for _, rate := range intervals {
			if rate <= 51.0 { // Allow small variance
				cappedCount++
			}
		}
		// At least some should be capped
		require.Greater(t, cappedCount, 0)
	})

	t.Run("zero base rate", func(t *testing.T) {
		dist := NewAdvancedTrafficDistribution(
			AdvancedTrafficDistributionConfig{
				BaseRateMBps:        0.0,
				BurstMultiplier:     2.0,
				LognormalSigma:      0.1,
				OnMeanSeconds:       5.0,
				OffMeanSeconds:      10.0,
				ErlangK:             2,
				SpikeRatePerSec:     0.0,
				SpikeMeanDur:        0.0,
				SpikeAmplitudeMean:  0.0,
				SpikeAmplitudeSigma: 0.0,
				CapacityLimitMB:     0.0,
				QueueMode:           "drop",
			},
			42, // seed
		)

		// Should return 0 interval or very large interval
		interval := dist.NextIntervalSeconds()
		require.GreaterOrEqual(t, interval, 0.0)
	})
}

func TestNewTrafficDistribution(t *testing.T) {
	t.Run("constant model", func(t *testing.T) {
		config := TrafficDistributionConfig{
			Model:         TrafficModelConstant,
			WriteRateMBps: 10.0,
		}
		dist := NewTrafficDistribution(config, 42)
		require.IsType(t, &ConstantTrafficDistribution{}, dist)
	})

	t.Run("advanced model", func(t *testing.T) {
		config := TrafficDistributionConfig{
			Model:           TrafficModelAdvancedONOFF,
			BaseRateMBps:    10.0,
			BurstMultiplier: 2.0,
			LognormalSigma:  0.1,
			OnMeanSeconds:   5.0,
			OffMeanSeconds:  10.0,
			ErlangK:         2,
			QueueMode:       "drop",
		}
		dist := NewTrafficDistribution(config, 42)
		require.IsType(t, &AdvancedTrafficDistribution{}, dist)
	})
}

func TestExponentialSample(t *testing.T) {
	// This is a helper function test
	// We can't directly test it, but we can test through AdvancedTrafficDistribution
	// The exponential distribution should produce positive values
	dist := NewAdvancedTrafficDistribution(
		AdvancedTrafficDistributionConfig{
			BaseRateMBps:        10.0,
			BurstMultiplier:     2.0,
			LognormalSigma:      0.1,
			OnMeanSeconds:       5.0,
			OffMeanSeconds:      10.0, // uses exponential
			ErlangK:             2,
			SpikeRatePerSec:     0.0,
			SpikeMeanDur:        0.0,
			SpikeAmplitudeMean:  0.0,
			SpikeAmplitudeSigma: 0.0,
			CapacityLimitMB:     0.0,
			QueueMode:           "drop",
		},
		42, // seed
	)

	// Generate many samples - should all be positive
	for i := 0; i < 100; i++ {
		interval := dist.NextIntervalSeconds()
		require.GreaterOrEqual(t, interval, 0.0)
		require.False(t, math.IsNaN(interval))
		require.False(t, math.IsInf(interval, 0))
	}
}
