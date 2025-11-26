package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAllocateJobSlot_FindsEarliestSlot tests that job allocation finds the slot that frees up earliest
func TestAllocateJobSlot_FindsEarliestSlot(t *testing.T) {
	config := DefaultConfig()
	config.MaxBackgroundJobs = 3
	config.WriteRateMBps = 0 // No writes
	config.TrafficDistribution.WriteRateMBps = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Initial state: all slots free
	require.Equal(t, 3, len(sim.backgroundJobSlots))
	require.Equal(t, 0.0, sim.backgroundJobSlots[0])
	require.Equal(t, 0.0, sim.backgroundJobSlots[1])
	require.Equal(t, 0.0, sim.backgroundJobSlots[2])

	// Allocate first job: 64 MB SSTable build @ 75 MB/s = 0.85s, 64 MB I/O @ 100 MB/s = 0.64s
	slotIdx, cpuStart, ioStart, completion := sim.allocateJobSlot(0.0, 0.85, 0.64)

	require.Equal(t, 0, slotIdx, "Should use slot 0 (first free slot)")
	require.Equal(t, 0.0, cpuStart, "CPU should start immediately")
	require.Equal(t, 0.85, ioStart, "I/O should start after CPU completes")
	require.Equal(t, 1.49, completion, "Should complete at 0.85 + 0.64 = 1.49s")
	require.Equal(t, 1.49, sim.backgroundJobSlots[0], "Slot 0 should be busy until 1.49s")
	require.Equal(t, 1.49, sim.diskBusyUntil, "Disk should be busy until 1.49s")

	// Allocate second job at T=0.5 (while first is still running)
	sim.virtualTime = 0.5
	slotIdx2, cpuStart2, ioStart2, completion2 := sim.allocateJobSlot(0.5, 0.85, 0.64)

	require.Equal(t, 1, slotIdx2, "Should use slot 1 (first slot busy)")
	require.Equal(t, 0.5, cpuStart2, "CPU should start immediately")
	require.Equal(t, 1.49, ioStart2, "I/O must wait for disk to be free (after job 1)")
	require.Equal(t, 2.13, completion2, "Should complete at 1.49 + 0.64 = 2.13s")
	require.Equal(t, 2.13, sim.backgroundJobSlots[1], "Slot 1 should be busy until 2.13s")
	require.Equal(t, 2.13, sim.diskBusyUntil, "Disk should be busy until 2.13s")

	// Allocate third job at T=0.7
	sim.virtualTime = 0.7
	slotIdx3, cpuStart3, ioStart3, completion3 := sim.allocateJobSlot(0.7, 0.85, 0.64)

	require.Equal(t, 2, slotIdx3, "Should use slot 2")
	require.Equal(t, 0.7, cpuStart3, "CPU should start immediately")
	require.Equal(t, 2.13, ioStart3, "I/O must wait for disk (after job 2)")
	require.Equal(t, 2.77, completion3, "Should complete at 2.13 + 0.64 = 2.77s")

	// NOW all 3 slots are busy
	t.Logf("All slots busy: [0]=%.2f, [1]=%.2f, [2]=%.2f",
		sim.backgroundJobSlots[0], sim.backgroundJobSlots[1], sim.backgroundJobSlots[2])
}

// TestAllocateJobSlot_WaitsForSlot tests that jobs must wait when all slots are busy
func TestAllocateJobSlot_WaitsForSlot(t *testing.T) {
	config := DefaultConfig()
	config.MaxBackgroundJobs = 2 // Only 2 slots
	config.WriteRateMBps = 0
	config.TrafficDistribution.WriteRateMBps = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Occupy both slots
	sim.virtualTime = 0.0
	_, _, _, _ = sim.allocateJobSlot(0.0, 0.85, 0.64) // Slot 0: completes at 1.49s
	sim.virtualTime = 0.3
	_, _, _, _ = sim.allocateJobSlot(0.3, 0.85, 0.64) // Slot 1: completes at 2.13s (I/O waits)

	// Verify both slots are busy
	require.Equal(t, 1.49, sim.backgroundJobSlots[0], "Slot 0 busy until 1.49s")
	require.Equal(t, 2.13, sim.backgroundJobSlots[1], "Slot 1 busy until 2.13s")

	// Third job arrives at T=0.6 - all slots busy!
	sim.virtualTime = 0.6
	slotIdx, cpuStart, ioStart, completion := sim.allocateJobSlot(0.6, 0.85, 0.64)

	// Should allocate the EARLIEST available slot (Slot 0, free at 1.49s)
	require.Equal(t, 0, slotIdx, "Should reuse slot 0 (earliest free)")
	require.Equal(t, 1.49, cpuStart, "CPU must wait for slot 0 to free up")
	require.Equal(t, 2.34, ioStart, "I/O starts after CPU completes (1.49 + 0.85 = 2.34)")
	require.Equal(t, 2.98, completion, "Completes at 2.34 + 0.64 = 2.98s")

	t.Logf("Job 3 arrival=%.2f, cpuStart=%.2f (waited %.2fs for slot), completion=%.2f",
		0.6, cpuStart, cpuStart-0.6, completion)
}

// TestBackgroundJobs_ConcurrentFlushes tests that multiple flushes can run concurrently
func TestBackgroundJobs_ConcurrentFlushes(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 200.0        // Fast writes
	config.IOThroughputMBps = 100.0     // Slower disk
	config.SSTableBuildThroughputMBps = 75.0
	config.MemtableFlushSizeMB = 64
	config.MaxWriteBufferNumber = 5     // Lots of buffer
	config.MaxBackgroundJobs = 3        // 3 concurrent flushes allowed
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.ReadWorkload = nil
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 200.0
	config.CompressionFactor = 1.0
	config.DeduplicationFactor = 1.0
	config.SimulationSpeedMultiplier = 1

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run for 2 seconds - should see concurrent flushes
	for i := 0; i < 2; i++ {
		sim.Step()
	}

	// At T=2s with 200 MB/s writes and 64 MB memtables:
	// - 6 memtables should have filled (200 * 2 / 64 = 6.25)
	// - With max_background_jobs=3, flushes should run 3 at a time
	// - Should have some immutable memtables waiting

	t.Logf("T=%.2fs: numImmutable=%d, diskBusyUntil=%.2fs, L0 files=%d",
		sim.VirtualTime(), sim.numImmutableMemtables, sim.diskBusyUntil, len(sim.lsm.Levels[0].Files))

	// Verify job slots are being used
	freeSlots := 0
	for i, busyUntil := range sim.backgroundJobSlots {
		t.Logf("Slot %d: busyUntil=%.2fs (free=%v)", i, busyUntil, busyUntil <= sim.VirtualTime())
		if busyUntil <= sim.VirtualTime() {
			freeSlots++
		}
	}

	// With concurrent operations, we should see flushes completing faster than pure serialization
	// Pure serialization: 6 flushes × 1.49s each = 8.94s
	// With 3 concurrent: ~3s (overlapping)
	require.Less(t, sim.diskBusyUntil, 5.0, "With 3 concurrent jobs, disk should not be backed up too far")
}

// TestBackgroundJobs_BlocksWhenAllSlotsBusy tests that operations block when max_background_jobs reached
func TestBackgroundJobs_BlocksWhenAllSlotsBusy(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 100.0
	config.IOThroughputMBps = 100.0
	config.SSTableBuildThroughputMBps = 75.0
	config.MemtableFlushSizeMB = 64
	config.MaxWriteBufferNumber = 10    // Lots of buffers (no stalls)
	config.MaxBackgroundJobs = 1        // Only 1 slot!
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.ReadWorkload = nil
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 100.0
	config.CompressionFactor = 1.0
	config.DeduplicationFactor = 1.0
	config.SimulationSpeedMultiplier = 1

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run for 3 seconds
	for i := 0; i < 3; i++ {
		sim.Step()
	}

	// At T=3s with 100 MB/s writes and 64 MB memtables:
	// - 4 memtables filled (100 * 3 / 64 = 4.68)
	// - With max_background_jobs=1, flushes are SERIALIZED
	// - Each flush: 0.85s CPU + 0.64s I/O = 1.49s
	// - 4 flushes × 1.49s = 5.96s total
	// - At T=3s, should still have ~2-3 immutable memtables waiting

	t.Logf("T=%.2fs: numImmutable=%d, diskBusyUntil=%.2fs, L0 files=%d",
		sim.VirtualTime(), sim.numImmutableMemtables, sim.diskBusyUntil, len(sim.lsm.Levels[0].Files))

	// With only 1 slot, diskBusyUntil should be significantly in the future
	require.Greater(t, sim.diskBusyUntil, 4.0, "With 1 job slot, flushes should be serialized")
	require.Greater(t, sim.numImmutableMemtables, 0, "Should have queued immutable memtables")

	// Verify only 1 slot exists
	require.Equal(t, 1, len(sim.backgroundJobSlots))
}

// TestAllocateJobSlot_CPUAndIOPhases tests the two-phase CPU/IO model
func TestAllocateJobSlot_CPUAndIOPhases(t *testing.T) {
	config := DefaultConfig()
	config.MaxBackgroundJobs = 2
	config.WriteRateMBps = 0
	config.TrafficDistribution.WriteRateMBps = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Test 1: CPU phase completes before disk is free
	// Job arrives at T=0, CPU takes 1s, I/O takes 0.5s
	sim.virtualTime = 0.0
	sim.diskBusyUntil = 0.5 // Disk busy until T=0.5

	slotIdx, cpuStart, ioStart, completion := sim.allocateJobSlot(0.0, 1.0, 0.5)

	require.Equal(t, 0, slotIdx)
	require.Equal(t, 0.0, cpuStart, "CPU starts immediately")
	require.Equal(t, 1.0, ioStart, "I/O starts when CPU done (CPU takes longer than disk wait)")
	require.Equal(t, 1.5, completion, "Completes at 1.0 + 0.5 = 1.5s")
	require.Equal(t, 1.5, sim.diskBusyUntil, "Disk busy until completion")

	// Test 2: Disk busy longer than CPU phase
	// Job arrives at T=2, CPU takes 0.5s, I/O takes 0.5s, but disk busy until T=3
	sim.virtualTime = 2.0
	sim.diskBusyUntil = 3.0 // Disk busy until T=3

	slotIdx2, cpuStart2, ioStart2, completion2 := sim.allocateJobSlot(2.0, 0.5, 0.5)

	require.Equal(t, 1, slotIdx2, "Should use slot 1")
	require.Equal(t, 2.0, cpuStart2, "CPU starts immediately")
	require.Equal(t, 3.0, ioStart2, "I/O must wait for disk (disk busy > CPU complete)")
	require.Equal(t, 3.5, completion2, "Completes at 3.0 + 0.5 = 3.5s")
	require.Equal(t, 3.5, sim.diskBusyUntil, "Disk busy until completion")
}

// TestBackgroundJobs_ComparisonWithPureSerialization tests the benefit of job slots
func TestBackgroundJobs_ComparisonWithPureSerialization(t *testing.T) {
	// Scenario: 3 flushes arrive quickly, each needs 0.85s CPU + 0.64s I/O

	// With max_background_jobs=1 (pure serialization)
	t.Run("max_background_jobs=1 (serialized)", func(t *testing.T) {
		config := DefaultConfig()
		config.MaxBackgroundJobs = 1
		config.WriteRateMBps = 0
		config.TrafficDistribution.WriteRateMBps = 0

		sim, err := NewSimulator(config)
		require.NoError(t, err)

		// Allocate 3 jobs
		sim.virtualTime = 0.0
		_, _, _, c1 := sim.allocateJobSlot(0.0, 0.85, 0.64)
		sim.virtualTime = 0.3
		_, _, _, c2 := sim.allocateJobSlot(0.3, 0.85, 0.64)
		sim.virtualTime = 0.6
		_, _, _, c3 := sim.allocateJobSlot(0.6, 0.85, 0.64)

		// All jobs use same slot - pure serialization
		// Job 1: 0.0 - 1.49s
		// Job 2: 1.49 - 2.98s (waits for slot)
		// Job 3: 2.98 - 4.47s (waits for slot)
		require.Equal(t, 1.49, c1)
		require.Equal(t, 2.98, c2)
		require.Equal(t, 4.47, c3)

		t.Logf("Serialized: Last job completes at %.2fs", c3)
	})

	// With max_background_jobs=3 (concurrent)
	t.Run("max_background_jobs=3 (concurrent)", func(t *testing.T) {
		config := DefaultConfig()
		config.MaxBackgroundJobs = 3
		config.WriteRateMBps = 0
		config.TrafficDistribution.WriteRateMBps = 0

		sim, err := NewSimulator(config)
		require.NoError(t, err)

		// Allocate 3 jobs at same times
		sim.virtualTime = 0.0
		_, _, _, c1 := sim.allocateJobSlot(0.0, 0.85, 0.64)
		sim.virtualTime = 0.3
		_, _, _, c2 := sim.allocateJobSlot(0.3, 0.85, 0.64)
		sim.virtualTime = 0.6
		_, _, _, c3 := sim.allocateJobSlot(0.6, 0.85, 0.64)

		// Jobs use different slots - CPU runs concurrently, I/O serializes
		// Job 1: CPU 0.0-0.85, I/O 0.85-1.49
		// Job 2: CPU 0.3-1.15, I/O 1.49-2.13 (waits for disk)
		// Job 3: CPU 0.6-1.45, I/O 2.13-2.77 (waits for disk)
		require.Equal(t, 1.49, c1)
		require.Equal(t, 2.13, c2)
		require.Equal(t, 2.77, c3)

		t.Logf("Concurrent: Last job completes at %.2fs (vs 4.47s serialized)", c3)

		// With 3 slots, should complete MUCH faster than serialization
		require.Less(t, c3, 4.0, "Concurrent should be faster than serialized (2.77 < 4.47)")
	})
}

// TestBackgroundJobs_RealWorldScenario tests realistic workload with background jobs
func TestBackgroundJobs_RealWorldScenario(t *testing.T) {
	config := DefaultConfig()
	config.WriteRateMBps = 50.0          // 50 MB/s writes
	config.IOThroughputMBps = 100.0      // 100 MB/s disk
	config.SSTableBuildThroughputMBps = 75.0
	config.MemtableFlushSizeMB = 64
	config.MaxWriteBufferNumber = 3
	config.MaxStalledWriteMemoryMB = 512
	config.MaxBackgroundJobs = 2         // 2 concurrent flushes
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.ReadWorkload = nil
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 50.0
	config.CompressionFactor = 1.0
	config.DeduplicationFactor = 1.0
	config.SimulationSpeedMultiplier = 1

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	err = sim.Reset()
	require.NoError(t, err)

	// Run for 10 seconds
	for i := 0; i < 10; i++ {
		sim.Step()
		if sim.metrics.IsOOMKilled {
			t.Fatalf("Unexpected OOM at T=%.2fs (write rate < disk capacity)", sim.VirtualTime())
		}
	}

	// With 50 MB/s writes and 100 MB/s disk capacity:
	// - System should be healthy (no OOM)
	// - Some stalls possible but should clear quickly
	// - L0 should have files

	require.False(t, sim.metrics.IsOOMKilled, "Should not OOM when write rate < disk capacity")
	require.Greater(t, len(sim.lsm.Levels[0].Files), 0, "Should have created L0 files")

	t.Logf("SUCCESS: T=%.2fs, L0 files=%d, numImmutable=%d, diskBusyUntil=%.2fs",
		sim.VirtualTime(), len(sim.lsm.Levels[0].Files), sim.numImmutableMemtables, sim.diskBusyUntil)
}
