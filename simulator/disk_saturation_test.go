package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDiskSaturation_ShouldStallAndOOM tests that writes exceeding disk capacity cause stalls and OOM
//
// EXPECTED BEHAVIOR:
// - Write rate: 200 MB/s
// - Disk capacity: 100 MB/s
// - Memtable size: 64 MB
// - Max write buffers: 3
//
// Timeline:
// T=0.00s: Write starts, memtable fills
// T=0.32s: First memtable full (64MB @ 200MB/s), starts flush
// T=0.64s: Second memtable full, starts flush
// T=0.96s: Third memtable full, starts flush
// T=1.28s: Fourth write arrives but all 3 buffers are full → STALL
//
// Disk saturation:
// - Flush 1: 64MB @ 100MB/s = 0.64s (T=0.32 - 0.96)
// - Flush 2: 64MB @ 100MB/s = 0.64s (T=0.64 - 1.28)
// - Flush 3: 64MB @ 100MB/s = 0.64s (T=0.96 - 1.60)
// - All 3 flushes overlap, need 192MB/s bandwidth but only have 100MB/s
//
// CURRENT BEHAVIOR (BROKEN):
// - Reserve() returns false but is ignored
// - Flushes proceed anyway (infinite disk bandwidth)
// - No stall, no OOM
//
// EXPECTED BEHAVIOR (CORRECT):
// - Disk saturates around T=1.0s
// - Writes stall
// - OOM within a few seconds as backlog grows
func TestDiskSaturation_ShouldStallAndOOM(t *testing.T) {
	// Start with defaults, then override critical fields
	config := DefaultConfig()

	// Critical: Write rate EXCEEDS disk capacity
	config.WriteRateMBps = 200.0          // 200 MB/s incoming
	config.IOThroughputMBps = 100.0       // 100 MB/s disk capacity
	config.MemtableFlushSizeMB = 64       // 64 MB memtables
	config.MaxWriteBufferNumber = 3       // Only 3 buffers
	config.MaxStalledWriteMemoryMB = 256  // OOM at 256MB backlog
	config.MaxBackgroundJobs = 10         // Plenty of flush capacity

	// Use leveled compaction with standard config
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7 // Standard LSM depth

	// Disable reads
	config.ReadWorkload = nil

	// Use constant traffic
	config.TrafficDistribution.Model = TrafficModelConstant
	config.TrafficDistribution.WriteRateMBps = 200.0

	// Disable compression for clarity
	config.CompressionFactor = 1.0
	config.DeduplicationFactor = 1.0

	// Run at 1x speed
	config.SimulationSpeedMultiplier = 1
	config.RandomSeed = 12345

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Initialize simulator with events (Reset schedules initial events)
	err = sim.Reset()
	require.NoError(t, err)

	// Run simulation for 10 seconds of virtual time
	// At 200MB/s with 100MB/s disk, should OOM quickly
	for i := 0; i < 10; i++ {
		sim.Step()

		t.Logf("[STEP %d] t=%.2fs, numImmutable=%d, stalled=%v, oom=%v, diskBusyUntil=%.2fs",
			i, sim.VirtualTime(), sim.numImmutableMemtables,
			sim.metrics.IsStalled, sim.metrics.IsOOMKilled,
			sim.GetDiskBusyUntil())

		if sim.metrics.IsOOMKilled {
			t.Logf("OOM occurred at t=%.2fs (expected!)", sim.VirtualTime())
			return // Test passes - OOM detected
		}
	}

	// BUG: Simulation ran for 10 seconds without OOM
	// With 200MB/s write rate and 100MB/s disk capacity, should have OOM'd
	t.Fatalf("BUG: Expected OOM due to disk saturation, but simulation ran for 10s without OOM. "+
		"numImmutable=%d, diskBusyUntil=%.2fs",
		sim.numImmutableMemtables, sim.GetDiskBusyUntil())
}
