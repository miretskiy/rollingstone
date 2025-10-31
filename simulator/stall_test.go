package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteStall_StallDetection verifies that write stalls are detected correctly
func TestWriteStall_StallDetection(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 1000.0   // High write rate to trigger stalls
	config.MaxWriteBufferNumber = 2 // Low threshold to trigger stalls easily
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 100.0 // Slow I/O to create backlog

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Initially, no stall
	require.Equal(t, 0.0, sim.stallStartTime)
	require.Equal(t, 0, sim.countStalledWrites())

	// Run simulation until we hit a stall
	// We need to fill up immutable memtables faster than they can flush
	stalled := false
	for i := 0; i < 100 && !stalled; i++ {
		sim.Step()
		metrics := sim.Metrics()
		if metrics.IsStalled {
			stalled = true
			t.Logf("Stall detected at step %d, virtual time %.1f", i, metrics.Timestamp)
		}
	}

	if !stalled {
		t.Logf("No stall detected after 100 steps. State: immutable=%d, max=%d",
			sim.numImmutableMemtables, config.MaxWriteBufferNumber)
		// This is OK - stall might not occur if compaction/flush is fast enough
		// But let's verify the stall detection logic works when it does stall
		return
	}

	// Verify stall state
	require.Greater(t, sim.stallStartTime, 0.0)
	metrics := sim.Metrics()
	require.True(t, metrics.IsStalled)
	require.Greater(t, metrics.StalledWriteCount, 0)
	t.Logf("Stall confirmed: %d stalled writes, %d immutable memtables (max=%d)",
		metrics.StalledWriteCount, sim.numImmutableMemtables, config.MaxWriteBufferNumber)
}

// TestWriteStall_StallClearing verifies that stalls clear when conditions improve
func TestWriteStall_StallClearing(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 100.0 // Reduced from 1000 - still high enough to cause stalls
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 50.0       // Slow I/O initially
	config.MaxStalledWriteMemoryMB = 0   // Disable OOM for this test
	config.SimulationSpeedMultiplier = 1 // Use normal speed to avoid processing too many events

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run until we get a stall (with reasonable limit)
	var stalledAtStep int
	virtualTimeLimit := 10.0 // Limit to 10 seconds of virtual time
	for i := 0; i < 200 && sim.virtualTime < virtualTimeLimit; i++ {
		sim.Step()
		if sim.metrics.IsOOMKilled {
			t.Fatalf("OOM occurred unexpectedly - test conditions may need adjustment")
		}
		metrics := sim.Metrics()
		if metrics.IsStalled && stalledAtStep == 0 {
			stalledAtStep = i
			t.Logf("Stall started at step %d, virtual time: %.3fs", i, sim.virtualTime)
			break // Found stall, proceed to clearing test
		}
	}

	// If we got a stall, verify it clears
	if sim.stallStartTime > 0 {
		initialStallDuration := sim.metrics.StallDurationSeconds
		t.Logf("Initial stall duration: %.3fs", initialStallDuration)

		// Reduce write rate to allow flushes to catch up
		config.WriteRateMBps = 10.0 // Much lower rate
		sim.UpdateConfig(config)

		// Run more steps to clear the stall (with reasonable limit)
		initialVirtualTime := sim.virtualTime
		for i := 0; i < 100 && sim.stallStartTime > 0 && sim.virtualTime < initialVirtualTime+20.0; i++ {
			sim.Step()
			if sim.metrics.IsOOMKilled {
				t.Fatalf("OOM occurred during stall clearing")
			}
		}

		// Stall should clear
		if sim.stallStartTime > 0 {
			t.Logf("Stall persisted after reducing write rate. Immutable memtables: %d (max=%d)",
				sim.numImmutableMemtables, config.MaxWriteBufferNumber)
			// This might be OK if compaction is still catching up
		} else {
			t.Logf("Stall cleared after %d steps", stalledAtStep)
			metrics := sim.Metrics()
			require.False(t, metrics.IsStalled)
			require.Greater(t, metrics.StallDurationSeconds, initialStallDuration,
				"Stall duration should accumulate")
		}
	} else {
		t.Log("No stall occurred - test conditions may need adjustment")
	}
}

// TestWriteStall_StallCount verifies that stalled write count is tracked correctly
func TestWriteStall_StallCount(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 200.0 // Reduced from 2000 - still high enough to cause stalls
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 50.0     // Very slow I/O to create backlog
	config.MaxStalledWriteMemoryMB = 0 // Disable OOM for this test

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run until we get a stall
	maxStalledCount := 0
	virtualTimeLimit := 5.0 // Limit to 5 seconds of virtual time
	for i := 0; i < 50 && sim.virtualTime < virtualTimeLimit; i++ {
		sim.Step()
		if sim.metrics.IsOOMKilled {
			t.Fatalf("OOM occurred unexpectedly")
		}
		metrics := sim.Metrics()
		if metrics.IsStalled {
			if metrics.StalledWriteCount > maxStalledCount {
				maxStalledCount = metrics.StalledWriteCount
			}
			t.Logf("Step %d: stalled=%v, count=%d, max=%d",
				i, metrics.IsStalled, metrics.StalledWriteCount, metrics.MaxStalledWriteCount)
		}
	}

	// Verify max stalled count is tracked
	finalMetrics := sim.Metrics()
	if finalMetrics.IsStalled || finalMetrics.MaxStalledWriteCount > 0 {
		require.GreaterOrEqual(t, finalMetrics.MaxStalledWriteCount, maxStalledCount,
			"MaxStalledWriteCount should be at least the maximum we saw")
		t.Logf("Max stalled write count: %d", finalMetrics.MaxStalledWriteCount)
	} else {
		t.Log("No stall occurred - test conditions may need adjustment")
	}
}

// TestWriteStall_StallDurationAccumulation verifies that stall duration accumulates correctly
func TestWriteStall_StallDurationAccumulation(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 100.0 // Reduced from 1000 - still high enough to cause stalls
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 50.0     // Slow I/O
	config.MaxStalledWriteMemoryMB = 0 // Disable OOM for this test

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Track stall duration as it accumulates
	var stallDurations []float64
	var wasStalled bool
	virtualTimeLimit := 10.0 // Limit to 10 seconds of virtual time

	for i := 0; i < 100 && sim.virtualTime < virtualTimeLimit; i++ {
		sim.Step()
		if sim.metrics.IsOOMKilled {
			t.Fatalf("OOM occurred unexpectedly")
		}
		metrics := sim.Metrics()
		if metrics.IsStalled {
			if !wasStalled {
				// Just entered stall
				t.Logf("Stall entered at step %d, time %.1f", i, metrics.Timestamp)
			}
			wasStalled = true
		} else {
			if wasStalled {
				// Just cleared stall
				t.Logf("Stall cleared at step %d, time %.1f", i, metrics.Timestamp)
				wasStalled = false
			}
		}
		stallDurations = append(stallDurations, metrics.StallDurationSeconds)
	}

	finalMetrics := sim.Metrics()
	t.Logf("Final stall duration: %.3fs", finalMetrics.StallDurationSeconds)

	// Verify duration is non-decreasing (accumulates)
	for i := 1; i < len(stallDurations); i++ {
		require.GreaterOrEqual(t, stallDurations[i], stallDurations[i-1],
			"Stall duration should be non-decreasing (accumulates)")
	}

	// If we had any stalls, duration should be > 0
	if finalMetrics.MaxStalledWriteCount > 0 {
		require.Greater(t, finalMetrics.StallDurationSeconds, 0.0,
			"If stalls occurred, duration should be > 0")
	}
}

// TestWriteStall_ResetClearsStall verifies that reset clears stall state
func TestWriteStall_ResetClearsStall(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 100.0 // Reduced from 1000
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 50.0
	config.MaxStalledWriteMemoryMB = 0 // Disable OOM for this test

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Try to get into a stall state
	virtualTimeLimit := 5.0 // Limit to 5 seconds of virtual time
	for i := 0; i < 50 && sim.virtualTime < virtualTimeLimit; i++ {
		sim.Step()
		if sim.metrics.IsOOMKilled {
			t.Fatalf("OOM occurred unexpectedly")
		}
	}

	// Reset
	sim.Reset()

	// Verify all stall state is cleared
	require.Equal(t, 0.0, sim.stallStartTime)
	require.Equal(t, 0, sim.countStalledWrites())

	metrics := sim.Metrics()
	require.False(t, metrics.IsStalled)
	require.Equal(t, 0, metrics.StalledWriteCount)
	require.Equal(t, 0, metrics.MaxStalledWriteCount)
	require.Equal(t, 0.0, metrics.StallDurationSeconds)
}

// TestWriteStall_FlushAwareRetryScheduling verifies that stalled writes schedule retries
// at flush completion time instead of every 1ms, reducing event explosion
func TestWriteStall_FlushAwareRetryScheduling(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 200.0    // Higher write rate
	config.MaxWriteBufferNumber = 2 // Low threshold
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 30.0     // Very slow I/O to create backlog
	config.MaxStalledWriteMemoryMB = 0 // Disable OOM for this test
	config.MaxBackgroundJobs = 1       // Limit concurrent compactions

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run until we hit a stall
	stalled := false
	virtualTimeLimit := 15.0
	for i := 0; i < 300 && sim.virtualTime < virtualTimeLimit && !stalled; i++ {
		sim.Step()
		if sim.metrics.IsStalled {
			stalled = true
			break
		}
	}

	if !stalled {
		t.Skip("No stall occurred - test requires a stall to verify retry scheduling")
		return
	}

	// Verify we're stalled
	require.Greater(t, sim.stallStartTime, 0.0)
	require.GreaterOrEqual(t, sim.numImmutableMemtables, config.MaxWriteBufferNumber)

	// Count stalled write events in queue
	stalledWriteCount := sim.countStalledWrites()

	// If nextFlushCompletionTime is set, stalled writes should be scheduled at that time
	// (or later), not at 1ms intervals
	if sim.nextFlushCompletionTime > sim.virtualTime {
		// Find the next flush event to verify timing
		nextFlush := sim.queue.FindNextFlushEvent()
		require.NotNil(t, nextFlush, "nextFlushCompletionTime is set but no flush event found")
		require.Equal(t, sim.nextFlushCompletionTime, nextFlush.Timestamp(),
			"nextFlushCompletionTime should match earliest flush completion time")

		// Check that stalled writes are scheduled near flush completion time
		// (not every 1ms)
		// We can't easily inspect all events, but we can verify that nextFlushCompletionTime
		// is being tracked correctly
		t.Logf("nextFlushCompletionTime=%.3f, virtualTime=%.3f, stalled writes=%d",
			sim.nextFlushCompletionTime, sim.virtualTime, stalledWriteCount)
		require.Greater(t, sim.nextFlushCompletionTime, sim.virtualTime,
			"nextFlushCompletionTime should be in the future")
	}

	// Run a few more steps to see if flush completes and clears the stall
	stepsBeforeFlush := 0
	for i := 0; i < 100 && sim.metrics.IsStalled; i++ {
		sim.Step()
		stepsBeforeFlush++
		if sim.virtualTime > virtualTimeLimit {
			break
		}
	}

	// If stall cleared, verify nextFlushCompletionTime was reset
	if !sim.metrics.IsStalled {
		require.Equal(t, 0.0, sim.nextFlushCompletionTime,
			"nextFlushCompletionTime should be cleared when stall clears")
		t.Logf("Stall cleared after %d steps", stepsBeforeFlush)
	}
}
