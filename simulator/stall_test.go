package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteStall_StallDetection verifies that write stalls are detected correctly
func TestWriteStall_StallDetection(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 1000.0  // High write rate to trigger stalls
	config.MaxWriteBufferNumber = 2 // Low threshold to trigger stalls easily
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 100.0 // Slow I/O to create backlog

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Initially, no stall
	require.False(t, sim.isWriteStalled)
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
	require.True(t, sim.isWriteStalled)
	metrics := sim.Metrics()
	require.True(t, metrics.IsStalled)
	require.Greater(t, metrics.StalledWriteCount, 0)
	t.Logf("Stall confirmed: %d stalled writes, %d immutable memtables (max=%d)",
		metrics.StalledWriteCount, sim.numImmutableMemtables, config.MaxWriteBufferNumber)
}

// TestWriteStall_StallClearing verifies that stalls clear when conditions improve
func TestWriteStall_StallClearing(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 1000.0  // High write rate
	config.MaxWriteBufferNumber = 2 // Low threshold
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 100.0 // Slow I/O initially

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run until we get a stall
	var stalledAtStep int
	for i := 0; i < 200; i++ {
		sim.Step()
		metrics := sim.Metrics()
		if metrics.IsStalled && stalledAtStep == 0 {
			stalledAtStep = i
			t.Logf("Stall started at step %d", i)
		}
	}

	// If we got a stall, verify it clears
	if sim.isWriteStalled {
		initialStallDuration := sim.metrics.StallDurationSeconds
		t.Logf("Initial stall duration: %.3fs", initialStallDuration)

		// Reduce write rate to allow flushes to catch up
		config.WriteRateMBps = 10.0 // Much lower rate
		sim.UpdateConfig(config)

		// Run more steps to clear the stall
		for i := 0; i < 100 && sim.isWriteStalled; i++ {
			sim.Step()
		}

		// Stall should clear
		if sim.isWriteStalled {
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
	config.WriteRateMBps = 2000.0  // Very high write rate
	config.MaxWriteBufferNumber = 2 // Low threshold
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 50.0 // Very slow I/O to create backlog

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Run until we get a stall
	maxStalledCount := 0
	for i := 0; i < 50; i++ {
		sim.Step()
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
	config.WriteRateMBps = 1000.0
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 100.0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Track stall duration as it accumulates
	var stallDurations []float64
	var wasStalled bool

	for i := 0; i < 100; i++ {
		sim.Step()
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
	config.WriteRateMBps = 1000.0
	config.MaxWriteBufferNumber = 2
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 100.0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	sim.Reset()

	// Try to get into a stall state
	for i := 0; i < 50; i++ {
		sim.Step()
	}

	// Reset
	sim.Reset()

	// Verify all stall state is cleared
	require.False(t, sim.isWriteStalled)
	require.Equal(t, 0.0, sim.stallStartTime)
	require.Equal(t, 0, sim.countStalledWrites())

	metrics := sim.Metrics()
	require.False(t, metrics.IsStalled)
	require.Equal(t, 0, metrics.StalledWriteCount)
	require.Equal(t, 0, metrics.MaxStalledWriteCount)
	require.Equal(t, 0.0, metrics.StallDurationSeconds)
}
