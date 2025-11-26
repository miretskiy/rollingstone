package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReset_ClearsAllState verifies that Reset() properly clears all simulator state
func TestReset_ClearsAllState(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 100
	config.MemtableFlushSizeMB = 64
	config.MaxWriteBufferNumber = 3
	config.MaxBackgroundJobs = 2
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.ReadWorkload = nil
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 100

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run for 5 seconds to build up state
	for i := 0; i < 5; i++ {
		sim.Step()
	}

	// Verify we have state
	require.Greater(t, sim.VirtualTime(), 0.0, "Should have advanced time")
	require.Greater(t, len(sim.lsm.Levels[0].Files), 0, "Should have created L0 files")

	// Save some state values
	oldTime := sim.VirtualTime()
	oldL0Count := len(sim.lsm.Levels[0].Files)

	// Reset the simulator
	err = sim.Reset()
	require.NoError(t, err)

	// Verify state is cleared
	require.Equal(t, 0.0, sim.VirtualTime(), "Virtual time should reset to 0")
	require.Equal(t, 0, len(sim.lsm.Levels[0].Files), "L0 should be empty")
	require.Equal(t, 0, sim.numImmutableMemtables, "No immutable memtables")
	require.Equal(t, 0.0, sim.diskBusyUntil, "Disk should be free")
	require.Equal(t, 0, len(sim.pendingCompactions), "No pending compactions")
	require.Equal(t, 1, sim.nextCompactionID, "Compaction ID should reset")
	require.Equal(t, 0.0, sim.stallStartTime, "No active stall")
	require.NotEmpty(t, sim.queue.Len(), "Queue should have events scheduled")

	// Verify background job slots reset
	for i, busyUntil := range sim.backgroundJobSlots {
		require.Equal(t, 0.0, busyUntil, "Job slot %d should be free", i)
	}

	t.Logf("Reset successful: time %.2f→0.0, L0 files %d→0", oldTime, oldL0Count)
}

// TestReset_PreservesConfig verifies that Reset() keeps the configuration
func TestReset_PreservesConfig(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 123.45
	config.MemtableFlushSizeMB = 128
	config.MaxBackgroundJobs = 7
	config.IOThroughputMBps = 500

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run simulation
	sim.Step()

	// Reset
	err = sim.Reset()
	require.NoError(t, err)

	// Verify config is preserved
	require.Equal(t, 123.45, sim.config.WriteRateMBps, "WriteRateMBps should be preserved")
	require.Equal(t, 128, sim.config.MemtableFlushSizeMB, "MemtableFlushSizeMB should be preserved")
	require.Equal(t, 7, sim.config.MaxBackgroundJobs, "MaxBackgroundJobs should be preserved")
	require.Equal(t, 500.0, sim.config.IOThroughputMBps, "IOThroughputMBps should be preserved")

	// Verify job slots match config
	require.Equal(t, 7, len(sim.backgroundJobSlots), "Should have 7 job slots matching config")
}

// TestReset_WithPendingFlushes verifies that immutable memtables are handled correctly
func TestReset_WithPendingFlushes(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 50
	config.MemtableFlushSizeMB = 64
	config.MaxWriteBufferNumber = 10 // Lots of buffers
	config.MaxBackgroundJobs = 1     // Slow flushing
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.ReadWorkload = nil
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 50

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run until we have immutable memtables
	for i := 0; i < 3; i++ {
		sim.Step()
		if sim.numImmutableMemtables > 0 {
			break
		}
	}

	immutableCount := sim.numImmutableMemtables
	if immutableCount == 0 {
		t.Skip("No immutable memtables created, can't test pending flush handling")
	}

	t.Logf("Before reset: %d immutable memtables", immutableCount)

	// Reset with pending memtables
	err = sim.Reset()
	require.NoError(t, err)

	// After reset, state should be completely fresh
	require.Equal(t, 0, sim.numImmutableMemtables, "Immutable memtables should reset to 0")
	require.Equal(t, 0, len(sim.immutableMemtableSizes), "Immutable sizes should be cleared")
	require.Equal(t, 0.0, sim.VirtualTime(), "Time should reset")

	t.Logf("After reset: All state cleared")
}
