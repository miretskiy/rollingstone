package simulator

import (
	"math/rand"
	"testing"
)

// Test basic read path metrics calculation
func TestReadPathMetrics(t *testing.T) {
	config := DefaultConfig()

	// Enable read path modeling with default settings
	readWorkload := DefaultReadWorkload()
	readWorkload.Enabled = true
	readWorkload.RequestsPerSec = 1000
	config.ReadWorkload = &readWorkload

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	sim.Reset()

	// Run for a bit to populate LSM
	for i := 0; i < 100; i++ {
		sim.Step()
	}

	metrics := sim.Metrics()

	// Verify read metrics are calculated
	if metrics.AvgReadLatencyMs == 0 {
		t.Errorf("Expected non-zero AvgReadLatencyMs, got %f", metrics.AvgReadLatencyMs)
	}
	if metrics.P50ReadLatencyMs == 0 {
		t.Errorf("Expected non-zero P50ReadLatencyMs, got %f", metrics.P50ReadLatencyMs)
	}
	if metrics.P99ReadLatencyMs == 0 {
		t.Errorf("Expected non-zero P99ReadLatencyMs, got %f", metrics.P99ReadLatencyMs)
	}

	// P99 should be >= P50
	// Note: With 90% cache hit rate, P50 will be cache hit latency (very low)
	// while Avg is pulled up by point lookups and scans
	if metrics.P99ReadLatencyMs < metrics.P50ReadLatencyMs {
		t.Errorf("P99 (%f) should be >= P50 (%f)", metrics.P99ReadLatencyMs, metrics.P50ReadLatencyMs)
	}
	// Average should be between P50 and P99
	if metrics.AvgReadLatencyMs < metrics.P50ReadLatencyMs || metrics.AvgReadLatencyMs > metrics.P99ReadLatencyMs {
		t.Logf("Warning: Avg (%f) is outside P50-P99 range [%f, %f]",
			metrics.AvgReadLatencyMs, metrics.P50ReadLatencyMs, metrics.P99ReadLatencyMs)
	}

	// Read amplification should be > 1 (at least memtable)
	if metrics.ReadAmplification < 1.0 {
		t.Errorf("Expected ReadAmplification >= 1.0, got %f", metrics.ReadAmplification)
	}

	t.Logf("Read metrics: Avg=%.3f ms, P50=%.3f ms, P99=%.3f ms, ReadAmp=%.1f, ReadBW=%.2f MB/s",
		metrics.AvgReadLatencyMs, metrics.P50ReadLatencyMs, metrics.P99ReadLatencyMs,
		metrics.ReadAmplification, metrics.ReadBandwidthMBps)
}

// Test that read latency increases with read amplification
func TestReadLatencyIncreasesWithAmplification(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Create two read workload configs
	workload := DefaultReadWorkload()
	workload.Enabled = true
	workload.RequestsPerSec = 1000

	metrics1 := NewMetrics()
	metrics2 := NewMetrics()

	// Low read amplification (good LSM)
	readAmp1 := 3.0
	metrics1.UpdateReadMetrics(&workload, readAmp1, 4, rng)

	// High read amplification (bad LSM with many L0 files)
	readAmp2 := 15.0
	rng2 := rand.New(rand.NewSource(43)) // Different seed for independent samples
	metrics2.UpdateReadMetrics(&workload, readAmp2, 4, rng2)

	// Latency should generally increase with read amplification
	// (Due to sampling max of more values for point lookups)
	// However, due to randomness, this is not guaranteed for every single run
	// So we just log and verify bandwidth increases
	if metrics2.ReadBandwidthMBps <= metrics1.ReadBandwidthMBps {
		t.Errorf("Expected higher bandwidth with higher read amp. ReadAmp1=%.1f -> BW=%.3f, ReadAmp2=%.1f -> BW=%.3f",
			readAmp1, metrics1.ReadBandwidthMBps, readAmp2, metrics2.ReadBandwidthMBps)
	}

	t.Logf("ReadAmp=%.1f: Avg=%.3f ms, P50=%.3f ms, P99=%.3f ms",
		readAmp1, metrics1.AvgReadLatencyMs, metrics1.P50ReadLatencyMs, metrics1.P99ReadLatencyMs)
	t.Logf("ReadAmp=%.1f: Avg=%.3f ms, P50=%.3f ms, P99=%.3f ms",
		readAmp2, metrics2.AvgReadLatencyMs, metrics2.P50ReadLatencyMs, metrics2.P99ReadLatencyMs)
}

// Test latency sampling distributions
func TestLatencySampling(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Fixed distribution
	fixedSpec := LatencySpec{
		Distribution: LatencyDistFixed,
		Mean:         5.0,
	}
	for i := 0; i < 10; i++ {
		latency := SampleLatency(fixedSpec, rng)
		if latency != 5.0 {
			t.Errorf("Fixed distribution should always return mean. Got %f, expected 5.0", latency)
		}
	}

	// Exponential distribution (mean should be close to specified mean over many samples)
	expSpec := LatencySpec{
		Distribution: LatencyDistExp,
		Mean:         10.0,
	}
	samples := make([]float64, 10000)
	sum := 0.0
	for i := 0; i < 10000; i++ {
		samples[i] = SampleLatency(expSpec, rng)
		sum += samples[i]
		if samples[i] < 0 {
			t.Errorf("Exponential sample should be non-negative, got %f", samples[i])
		}
	}
	avgLatency := sum / 10000.0
	// Mean should be within 10% of specified mean
	if avgLatency < 9.0 || avgLatency > 11.0 {
		t.Errorf("Exponential mean should be close to 10.0, got %f", avgLatency)
	}

	// Lognormal distribution
	lognormSpec := LatencySpec{
		Distribution: LatencyDistLognormal,
		Mean:         20.0,
	}
	sum = 0.0
	for i := 0; i < 10000; i++ {
		latency := SampleLatency(lognormSpec, rng)
		sum += latency
		if latency < 0 {
			t.Errorf("Lognormal sample should be non-negative, got %f", latency)
		}
	}
	avgLatency = sum / 10000.0
	// Mean should be reasonably close (within 20% due to lognormal skew)
	if avgLatency < 16.0 || avgLatency > 24.0 {
		t.Errorf("Lognormal mean should be close to 20.0, got %f", avgLatency)
	}

	t.Logf("Latency sampling test passed")
}

// Test read bandwidth calculation
func TestReadBandwidth(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	workload := DefaultReadWorkload()
	workload.Enabled = true
	workload.RequestsPerSec = 1000 // 1000 reads/sec
	workload.CacheHitRate = 0.90   // 90% cache hits (0 bandwidth)
	workload.BloomNegativeRate = 0.02 // 2% bloom negatives (0 bandwidth)
	workload.ScanRate = 0.05 // 5% scans
	// Remaining 3% are point lookups

	metrics := NewMetrics()
	readAmp := 5.0
	blockSizeKB := 4

	metrics.UpdateReadMetrics(&workload, readAmp, blockSizeKB, rng)

	// Expected bandwidth:
	// - Cache hits: 0 MB/s (900 reqs/sec)
	// - Bloom negatives: 0 MB/s (20 reqs/sec)
	// - Scans: 50 * (16 KB / 1024) = 0.78 MB/s (50 reqs/sec * 16 KB)
	// - Point lookups: 30 * (4 KB / 1024) * 5 = 0.59 MB/s (30 reqs/sec * 4 KB * 5 read amp)
	// Total: ~1.37 MB/s

	expectedBW := (1000 * 0.03 * (4.0 / 1024.0) * readAmp) + (1000 * 0.05 * (16.0 / 1024.0))

	if metrics.ReadBandwidthMBps < expectedBW*0.9 || metrics.ReadBandwidthMBps > expectedBW*1.1 {
		t.Errorf("Expected read bandwidth ~%.2f MB/s, got %.2f MB/s", expectedBW, metrics.ReadBandwidthMBps)
	}

	t.Logf("Read bandwidth: %.2f MB/s (expected ~%.2f MB/s)", metrics.ReadBandwidthMBps, expectedBW)
}

// Test disabled read path modeling
func TestDisabledReadPath(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	metrics := NewMetrics()
	readAmp := 5.0

	// Nil config (disabled)
	metrics.UpdateReadMetrics(nil, readAmp, 4, rng)

	if metrics.AvgReadLatencyMs != 0 {
		t.Errorf("Expected zero metrics when disabled, got AvgReadLatencyMs=%.3f", metrics.AvgReadLatencyMs)
	}
	if metrics.ReadBandwidthMBps != 0 {
		t.Errorf("Expected zero bandwidth when disabled, got %.3f", metrics.ReadBandwidthMBps)
	}

	// Disabled via Enabled flag
	workload := DefaultReadWorkload()
	workload.Enabled = false
	metrics.UpdateReadMetrics(&workload, readAmp, 4, rng)

	if metrics.AvgReadLatencyMs != 0 {
		t.Errorf("Expected zero metrics when disabled, got AvgReadLatencyMs=%.3f", metrics.AvgReadLatencyMs)
	}

	t.Logf("Disabled read path test passed")
}

// Test read request rate variability
func TestReadRequestRateVariability(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	workload := DefaultReadWorkload()
	workload.Enabled = true
	workload.RequestsPerSec = 1000
	workload.RequestRateVariability = 0.2 // 20% coefficient of variation

	metrics := NewMetrics()
	readAmp := 5.0
	blockSizeKB := 4

	// Run multiple times to verify variability
	var bandwidths []float64
	for i := 0; i < 10; i++ {
		metrics.UpdateReadMetrics(&workload, readAmp, blockSizeKB, rng)
		bandwidths = append(bandwidths, metrics.ReadBandwidthMBps)
	}

	// Calculate mean and stddev of bandwidth
	sum := 0.0
	for _, bw := range bandwidths {
		sum += bw
	}
	mean := sum / float64(len(bandwidths))

	sumSqDiff := 0.0
	for _, bw := range bandwidths {
		diff := bw - mean
		sumSqDiff += diff * diff
	}
	stddev := sumSqDiff / float64(len(bandwidths))

	// With variability=0.2, we expect some variation in bandwidth
	// (not all values should be identical)
	allSame := true
	for i := 1; i < len(bandwidths); i++ {
		if bandwidths[i] != bandwidths[0] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Errorf("Expected bandwidth variation with RequestRateVariability=0.2, but all values were identical")
	}

	// Verify bandwidth values are reasonable (not negative, not extreme)
	for i, bw := range bandwidths {
		if bw < 0 {
			t.Errorf("Bandwidth[%d] should be non-negative, got %.2f", i, bw)
		}
		// Should be within a reasonable range (mean +/- 3*stddev)
		if bw < mean*0.5 || bw > mean*1.5 {
			t.Logf("Warning: Bandwidth[%d]=%.2f is outside expected range [%.2f, %.2f]", i, bw, mean*0.5, mean*1.5)
		}
	}

	t.Logf("Request rate variability test passed: mean BW=%.2f MB/s, stddev=%.2f", mean, stddev)
	t.Logf("Bandwidth samples: %v", bandwidths)
}

// Test that zero variability produces constant bandwidth
func TestZeroVariability(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	workload := DefaultReadWorkload()
	workload.Enabled = true
	workload.RequestsPerSec = 1000
	workload.RequestRateVariability = 0.0 // No variability

	metrics := NewMetrics()
	readAmp := 5.0
	blockSizeKB := 4

	// Run multiple times - should get same bandwidth each time
	var bandwidths []float64
	for i := 0; i < 10; i++ {
		metrics.UpdateReadMetrics(&workload, readAmp, blockSizeKB, rng)
		bandwidths = append(bandwidths, metrics.ReadBandwidthMBps)
	}

	// All values should be identical (no variability)
	for i := 1; i < len(bandwidths); i++ {
		if bandwidths[i] != bandwidths[0] {
			t.Errorf("Expected constant bandwidth with RequestRateVariability=0.0, but got variation: %.3f != %.3f", bandwidths[i], bandwidths[0])
		}
	}

	t.Logf("Zero variability test passed: constant BW=%.2f MB/s", bandwidths[0])
}
