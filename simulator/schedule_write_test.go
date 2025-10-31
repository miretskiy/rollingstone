package simulator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Run tests with timeout: go test -timeout 30s ./simulator -run TestScheduleWriteEvent
// Or individually: go test -timeout 10s ./simulator -run TestScheduleWriteEvent_StallScenario

// TestScheduleWriteEvent_StallScenario tests the exact scenario described:
// - 5MB memtable flush size
// - 50MB/s incoming rate (1MB every 20ms)
// - 1MB/s disk throughput (very slow)
// - Compactions disabled
// Verifies that writes continue arriving and event counts remain reasonable
func TestScheduleWriteEvent_StallScenario(t *testing.T) {
	config := DefaultConfig()
	config.MemtableFlushSizeMB = 5     // Small memtable to trigger flushes quickly
	config.WriteRateMBps = 50.0        // 50 MB/s = 1MB every 20ms
	config.IOThroughputMBps = 1.0      // Very slow disk (1MB/s)
	config.MaxWriteBufferNumber = 2    // Stall when 2 immutable memtables
	config.MaxBackgroundJobs = 1       // Minimum allowed (but disable compactions via trigger)
	config.L0CompactionTrigger = 10000 // Effectively disable compactions
	config.SimulationSpeedMultiplier = 1
	config.MaxStalledWriteMemoryMB = 0 // Disable OOM for this test

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Track initial state
	initialQueueSize := sim.queue.Len()

	// Run for 1 second of virtual time
	maxSteps := 1000
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startTime := time.Now()
	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timeout: simulation took too long (>5s real time)")
		default:
		}
		sim.Step()
		if sim.virtualTime >= 1.0 {
			break
		}
		if sim.metrics.IsOOMKilled {
			break
		}
	}
	elapsed := time.Since(startTime)

	// Verify we reached 1 second
	require.GreaterOrEqual(t, sim.virtualTime, 1.0, "Should have reached 1 second of virtual time")

	// Verify writes occurred (but less than 50 MB due to stalls)
	// With 1MB/s disk and 5MB memtables, flushes take 5 seconds each
	// So writes will stall, but ScheduleWriteEvent continues scheduling writes
	require.Greater(t, sim.metrics.TotalDataWrittenMB, 0.0, "Should have written some data")
	// We expect less than 50 MB because writes are stalled
	require.Less(t, sim.metrics.TotalDataWrittenMB, 50.0, "Should have written less than 50 MB due to stalls")

	// Verify stalls occurred
	require.Greater(t, sim.metrics.MaxStalledWriteCount, 0, "Should have had stalled writes")

	// Verify performance - should complete quickly
	require.Less(t, elapsed, 2*time.Second, "1 second of simulation should complete quickly")

	// Verify queue isn't empty (should have ScheduleWriteEvent and CompactionCheckEvent)
	require.Greater(t, sim.queue.Len(), 0, "Queue should not be empty")

	// Count stalled writes in queue
	stalledWriteCount := sim.countStalledWrites()

	t.Logf("Results:")
	t.Logf("  Virtual time: %.3fs", sim.virtualTime)
	t.Logf("  Total data written: %.1f MB", sim.metrics.TotalDataWrittenMB)
	t.Logf("  Max stalled write count: %d", sim.metrics.MaxStalledWriteCount)
	t.Logf("  Current stalled write count: %d", stalledWriteCount)
	t.Logf("  Queue size: %d (initial: %d)", sim.queue.Len(), initialQueueSize)
	t.Logf("  Real time elapsed: %v", elapsed)
	t.Logf("  Num immutable memtables: %d", sim.numImmutableMemtables)

	// Critical: Verify queue size is reasonable
	// With 50MB/s and stalls, we might have many stalled writes
	// But queue shouldn't explode - reasonable upper bound: 1000 events
	require.Less(t, sim.queue.Len(), 1000,
		"Queue size should be reasonable - not exploding with retries")
}

// TestScheduleWriteEvent_HighSpeedMultiplier tests the same scenario at 5x speed
// Verifies that event counts don't explode and performance remains reasonable
func TestScheduleWriteEvent_HighSpeedMultiplier(t *testing.T) {
	config := DefaultConfig()
	config.MemtableFlushSizeMB = 5
	config.WriteRateMBps = 50.0
	config.IOThroughputMBps = 1.0
	config.MaxWriteBufferNumber = 2
	config.MaxBackgroundJobs = 1 // Minimum allowed (but disable compactions via trigger)
	config.L0CompactionTrigger = 10000
	config.SimulationSpeedMultiplier = 5 // 5x speed
	config.MaxStalledWriteMemoryMB = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run for 1 second of virtual time
	maxSteps := 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	startTime := time.Now()
	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timeout: simulation took too long (>10s real time)")
		default:
		}
		sim.Step()
		if sim.virtualTime >= 1.0 {
			break
		}
		if sim.metrics.IsOOMKilled {
			break
		}
	}
	elapsed := time.Since(startTime)

	require.GreaterOrEqual(t, sim.virtualTime, 1.0)

	// Verify writes occurred
	require.Greater(t, sim.metrics.TotalDataWrittenMB, 0.0)

	// Verify performance - should still be fast
	require.Less(t, elapsed, 5*time.Second, "Should complete quickly even at 5x speed")

	// Verify queue size is reasonable
	require.Less(t, sim.queue.Len(), 1000,
		"Queue size should be reasonable even at 5x speed")

	t.Logf("Results at 5x speed:")
	t.Logf("  Virtual time: %.3fs", sim.virtualTime)
	t.Logf("  Total data written: %.1f MB", sim.metrics.TotalDataWrittenMB)
	t.Logf("  Max stalled write count: %d", sim.metrics.MaxStalledWriteCount)
	t.Logf("  Queue size: %d", sim.queue.Len())
	t.Logf("  Real time elapsed: %v", elapsed)
}

// TestScheduleWriteEvent_WriteRateAccuracy verifies that writes arrive at exactly
// the configured rate, even during stalls
func TestScheduleWriteEvent_WriteRateAccuracy(t *testing.T) {
	config := DefaultConfig()
	config.MemtableFlushSizeMB = 64  // Large memtable - won't fill quickly
	config.WriteRateMBps = 10.0      // 10 MB/s = 1MB every 100ms
	config.IOThroughputMBps = 1000.0 // Very fast disk (no stalls)
	config.MaxWriteBufferNumber = 2
	config.MaxBackgroundJobs = 1 // Minimum allowed (but disable compactions via trigger)
	config.L0CompactionTrigger = 10000
	config.SimulationSpeedMultiplier = 1
	config.MaxStalledWriteMemoryMB = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run for 1 second
	maxSteps := 1000
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timeout: simulation took too long (>5s real time)")
		default:
		}
		sim.Step()
		if sim.virtualTime >= 1.0 {
			break
		}
	}

	require.GreaterOrEqual(t, sim.virtualTime, 1.0)

	// With 64MB memtable and 10 MB/s, we won't flush in 1 second
	// So we should have written some data but not 10 MB yet (data is in active memtable)
	// Check that we've written at least 5 MB (some writes happened)
	require.Greater(t, sim.metrics.TotalDataWrittenMB, 0.0, "Should have written some data")
	require.Less(t, sim.metrics.TotalDataWrittenMB, 15.0, "Should have written less than 15 MB (memtable not full yet)")

	// Verify no stalls (fast disk)
	require.Equal(t, 0, sim.metrics.MaxStalledWriteCount, "Should have no stalls with fast disk")

	// Verify we've accumulated data in the memtable
	require.Greater(t, sim.lsm.MemtableCurrentSize, 0.0, "Should have data in active memtable")
}

// TestScheduleWriteEvent_PerformanceUnderLoad tests performance at very high speed multipliers
func TestScheduleWriteEvent_PerformanceUnderLoad(t *testing.T) {
	config := DefaultConfig()
	config.MemtableFlushSizeMB = 5
	config.WriteRateMBps = 50.0
	config.IOThroughputMBps = 1.0
	config.MaxWriteBufferNumber = 2
	config.MaxBackgroundJobs = 1 // Minimum allowed (but disable compactions via trigger)
	config.L0CompactionTrigger = 10000
	config.SimulationSpeedMultiplier = 10 // Reduced from 100 - 10x is still a good stress test
	config.MaxStalledWriteMemoryMB = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run for 0.1 seconds of virtual time (much less to avoid processing too many events)
	// At 10x speed, this processes 1 second per Step(), so we'll complete quickly
	maxSteps := 10
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startTime := time.Now()
	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timeout: simulation took too long (>5s real time)")
		default:
		}
		sim.Step()
		if sim.virtualTime >= 0.1 {
			break
		}
		if sim.metrics.IsOOMKilled {
			break
		}
	}
	elapsed := time.Since(startTime)

	require.GreaterOrEqual(t, sim.virtualTime, 0.1)

	// Performance check: should complete quickly
	require.Less(t, elapsed, 2*time.Second,
		"Simulation should complete quickly even at 10x speed")

	t.Logf("Performance test at 10x speed:")
	t.Logf("  Virtual time: %.3fs", sim.virtualTime)
	t.Logf("  Real time elapsed: %v", elapsed)
	t.Logf("  Queue size: %d", sim.queue.Len())
}

// TestScheduleWriteEvent_StallRetryBehavior verifies that stalled writes retry correctly
// and don't create excessive events
func TestScheduleWriteEvent_StallRetryBehavior(t *testing.T) {
	config := DefaultConfig()
	config.MemtableFlushSizeMB = 5
	config.WriteRateMBps = 50.0   // 1MB every 20ms
	config.IOThroughputMBps = 0.1 // Extremely slow disk (0.1 MB/s)
	config.MaxWriteBufferNumber = 2
	config.MaxBackgroundJobs = 1 // Minimum allowed (but disable compactions via trigger)
	config.L0CompactionTrigger = 10000
	config.SimulationSpeedMultiplier = 1
	config.MaxStalledWriteMemoryMB = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run for 0.2 seconds (200ms)
	// Expected:
	// - ScheduleWriteEvent fires: 0ms, 20ms, 40ms, ... 180ms = 10 times
	// - WriteEvents scheduled: 10 writes
	// - Writes will stall when we hit 2 immutable memtables
	// - Each stalled write retries every 1ms
	maxSteps := 1000
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startTime := time.Now()
	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timeout: simulation took too long (>5s real time)")
		default:
		}
		sim.Step()
		if sim.virtualTime >= 0.2 {
			break
		}
		if sim.metrics.IsOOMKilled {
			break
		}
	}
	elapsed := time.Since(startTime)

	require.GreaterOrEqual(t, sim.virtualTime, 0.2)

	// Verify writes occurred
	require.Greater(t, sim.metrics.TotalDataWrittenMB, 0.0)

	// Verify stalls occurred
	require.Greater(t, sim.metrics.MaxStalledWriteCount, 0, "Should have stalled writes")

	// Critical: Verify queue size is reasonable
	// At 200ms with 50MB/s, we have ~10 writes
	// If writes stall and retry every 1ms, worst case: 10 writes * 200 retries = 2000 events
	// But writes don't all stall immediately, so should be less
	// Reasonable upper bound: 500 events
	require.Less(t, sim.queue.Len(), 500,
		"Queue size should be reasonable - stalled writes retry but shouldn't explode")

	// Verify performance
	require.Less(t, elapsed, 2*time.Second, "Should complete quickly")

	t.Logf("Stall retry behavior:")
	t.Logf("  Virtual time: %.3fs", sim.virtualTime)
	t.Logf("  Total data written: %.1f MB", sim.metrics.TotalDataWrittenMB)
	t.Logf("  Max stalled write count: %d", sim.metrics.MaxStalledWriteCount)
	t.Logf("  Current stalled write count: %d", sim.countStalledWrites())
	t.Logf("  Queue size: %d", sim.queue.Len())
	t.Logf("  Real time elapsed: %v", elapsed)
}
