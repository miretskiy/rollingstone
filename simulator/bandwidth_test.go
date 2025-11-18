package simulator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParallelCompactions_ReserveBandwidth tests that multiple compactions
// can reserve bandwidth simultaneously up to the disk capacity
func TestParallelCompactions_ReserveBandwidth(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleLeveled
	config.MaxBackgroundJobs = 4  // Allow 4 parallel compactions
	config.IOThroughputMBps = 100 // 100 MB/s total disk bandwidth
	config.WriteRateMBps = 0      // No writes to simplify test
	config.TrafficDistribution.WriteRateMBps = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Populate L1 and L2 to trigger multiple compactions
	for i := 0; i < 20; i++ {
		file := &SSTFile{
			ID:        fmt.Sprintf("L1-%d", i),
			SizeMB:    64.0,
			CreatedAt: 0.0,
		}
		sim.lsm.Levels[1].AddFile(file)
	}
	for i := 0; i < 100; i++ {
		file := &SSTFile{
			ID:        fmt.Sprintf("L2-%d", i),
			SizeMB:    128.0,
			CreatedAt: 0.0,
		}
		sim.lsm.Levels[2].AddFile(file)
	}

	// Record initial available bandwidth
	initialBandwidth := sim.disk.AvailableBandwidthMBps
	require.Equal(t, float64(config.IOThroughputMBps), initialBandwidth)

	// Try to schedule multiple compactions
	scheduled := 0
	for i := 0; i < config.MaxBackgroundJobs; i++ {
		if sim.tryScheduleCompaction() {
			scheduled++
		}
	}

	// Verify that compactions were scheduled and bandwidth was reserved
	require.Greater(t, scheduled, 0, "At least one compaction should be scheduled")
	require.Less(t, sim.disk.AvailableBandwidthMBps, initialBandwidth, "Bandwidth should be reserved")

	// Verify that total reserved bandwidth doesn't exceed disk capacity
	reservedBandwidth := initialBandwidth - sim.disk.AvailableBandwidthMBps
	require.LessOrEqual(t, reservedBandwidth, initialBandwidth, "Reserved bandwidth should not exceed capacity")

	t.Logf("Scheduled %d compactions, reserved %.2f MB/s of %.2f MB/s total bandwidth",
		scheduled, reservedBandwidth, initialBandwidth)
}

// TestBandwidthRefunding_OnCompletion tests that bandwidth is properly refunded
// when flushes and compactions complete
func TestBandwidthRefunding_OnCompletion(t *testing.T) {
	config := DefaultConfig()
	config.IOThroughputMBps = 100

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Manually schedule a flush event to test bandwidth refunding
	// (using real write path would require queue management)
	flushSize := 64.0
	flushDuration := flushSize / float64(config.IOThroughputMBps)
	flushStartTime := sim.virtualTime
	flushCompleteTime := flushStartTime + flushDuration

	// Reserve bandwidth for flush
	flushBandwidth := flushSize / flushDuration
	sim.disk.Reserve(flushBandwidth)
	bandwidthAfterReserve := sim.disk.AvailableBandwidthMBps

	// Create and push flush event
	flushEvent := NewFlushEvent(flushCompleteTime, flushStartTime, flushSize)
	flushEvent.SetBandwidthMBps(flushBandwidth)

	t.Logf("Flush bandwidth reserved: %.2f MB/s, remaining: %.2f MB/s",
		flushBandwidth, bandwidthAfterReserve)

	// Process the flush event directly (bypassing Step() which requires perpetual events)
	sim.processFlush(flushEvent)

	bandwidthAfterRefund := sim.disk.AvailableBandwidthMBps

	// Bandwidth should be refunded after flush completes
	require.Greater(t, bandwidthAfterRefund, bandwidthAfterReserve,
		"Bandwidth should be refunded after flush completes")
	t.Logf("Flush completed: bandwidth refunded from %.2f to %.2f MB/s",
		bandwidthAfterReserve, bandwidthAfterRefund)
}

// TestTokenBucketSaturation tests behavior when disk bandwidth is fully saturated
func TestTokenBucketSaturation(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleLeveled
	config.MaxBackgroundJobs = 10 // Allow many parallel jobs
	config.IOThroughputMBps = 50  // Limited bandwidth
	config.WriteRateMBps = 0      // No writes
	config.TrafficDistribution.WriteRateMBps = 0

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Populate levels to trigger many compactions
	for i := 0; i < 30; i++ {
		file := &SSTFile{
			ID:        fmt.Sprintf("L1-%d", i),
			SizeMB:    64.0,
			CreatedAt: 0.0,
		}
		sim.lsm.Levels[1].AddFile(file)
	}
	for i := 0; i < 100; i++ {
		file := &SSTFile{
			ID:        fmt.Sprintf("L2-%d", i),
			SizeMB:    128.0,
			CreatedAt: 0.0,
		}
		sim.lsm.Levels[2].AddFile(file)
	}

	// Schedule compactions until bandwidth is saturated
	scheduled := 0
	for i := 0; i < config.MaxBackgroundJobs; i++ {
		if sim.tryScheduleCompaction() {
			scheduled++
		} else {
			break
		}
	}

	t.Logf("Scheduled %d compactions with %.2f MB/s remaining bandwidth",
		scheduled, sim.disk.AvailableBandwidthMBps)

	// Verify that we can't over-reserve bandwidth
	// Remaining bandwidth should be >= 0
	require.GreaterOrEqual(t, sim.disk.AvailableBandwidthMBps, 0.0,
		"Available bandwidth should never be negative")
}

// TestWALEvent_Lifecycle tests that WAL write events properly reserve and refund bandwidth
func TestWALEvent_Lifecycle(t *testing.T) {
	config := DefaultConfig()
	config.EnableWAL = true
	config.WALSync = false
	config.IOThroughputMBps = 100

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	initialBandwidth := sim.disk.AvailableBandwidthMBps

	// Manually test WAL bandwidth lifecycle
	walSize := 10.0
	walDuration := walSize / float64(config.IOThroughputMBps)
	walStartTime := sim.virtualTime
	walCompleteTime := walStartTime + walDuration

	// Reserve bandwidth for WAL
	walBandwidth := walSize / walDuration
	sim.disk.Reserve(walBandwidth)
	bandwidthAfterReserve := sim.disk.AvailableBandwidthMBps

	require.Less(t, bandwidthAfterReserve, initialBandwidth,
		"Bandwidth should be reserved for WAL write")
	t.Logf("WAL bandwidth reserved: %.2f MB/s used, %.2f MB/s remaining",
		initialBandwidth-bandwidthAfterReserve, bandwidthAfterReserve)

	// Create and process WAL event
	walEvent := NewWALWriteEvent(walCompleteTime, walStartTime, walSize)
	walEvent.SetBandwidthMBps(walBandwidth)

	// Process WAL completion directly
	sim.processWALWrite(walEvent)

	bandwidthAfterRefund := sim.disk.AvailableBandwidthMBps

	// Bandwidth should be refunded
	require.Greater(t, bandwidthAfterRefund, bandwidthAfterReserve,
		"Bandwidth should be refunded after WAL write completes")
	t.Logf("WAL completed: bandwidth refunded from %.2f to %.2f MB/s",
		bandwidthAfterReserve, bandwidthAfterRefund)
}

// TestReadBatchEvent_Lifecycle tests that read batch events properly reserve and refund bandwidth
func TestReadBatchEvent_Lifecycle(t *testing.T) {
	config := DefaultConfig()
	config.IOThroughputMBps = 100

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	initialBandwidth := sim.disk.AvailableBandwidthMBps

	// Manually test read batch bandwidth lifecycle
	// Simulate 1000 requests: 500 point lookups, 100 scans, 300 cache hits, 100 bloom negatives
	totalRequests := 1000
	pointLookups := 300
	scans := 100
	cacheHits := 500
	bloomNegatives := 100

	// Calculate bandwidth (similar to processScheduleRead)
	readAmp := 5.0              // Assume 5 files to check
	blockSizeMB := 4.0 / 1024.0 // 4KB blocks
	scanSizeMB := 16.0 / 1024.0 // 16KB scans

	pointLookupMB := float64(pointLookups) * blockSizeMB * readAmp
	scanMB := float64(scans) * scanSizeMB
	totalReadMB := pointLookupMB + scanMB

	readDuration := totalReadMB / float64(config.IOThroughputMBps)
	readBandwidth := totalReadMB / readDuration

	// Reserve bandwidth
	sim.disk.Reserve(readBandwidth)
	bandwidthAfterReserve := sim.disk.AvailableBandwidthMBps

	require.Less(t, bandwidthAfterReserve, initialBandwidth,
		"Bandwidth should be reserved for read batch")
	t.Logf("Read batch bandwidth reserved: %.2f MB/s used, %.2f MB/s remaining",
		initialBandwidth-bandwidthAfterReserve, bandwidthAfterReserve)

	// Create and process read batch event
	readEvent := NewReadBatchEvent(sim.virtualTime+readDuration, sim.virtualTime, totalRequests, pointLookups, scans, cacheHits, bloomNegatives)
	readEvent.SetBandwidthMBps(readBandwidth)

	// Process read batch completion directly
	sim.processReadBatch(readEvent)

	bandwidthAfterRefund := sim.disk.AvailableBandwidthMBps

	// Bandwidth should be refunded
	require.Greater(t, bandwidthAfterRefund, bandwidthAfterReserve,
		"Bandwidth should be refunded after read batch completes")
	t.Logf("Read batch completed: bandwidth refunded from %.2f to %.2f MB/s",
		bandwidthAfterReserve, bandwidthAfterRefund)
}
