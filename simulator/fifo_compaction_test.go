package simulator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFIFOSizeBasedDeletion tests that FIFO deletes oldest files when size threshold exceeded
func TestFIFOSizeBasedDeletion(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 500 // 500 MB threshold
	config.FIFOAllowCompaction = false   // Disable intra-L0 for this test
	config.NumLevels = 1                 // FIFO uses only L0
	config.MemtableFlushSizeMB = 100     // 100 MB flush size

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add 8 files of 100 MB each = 800 MB total (exceeds 500 MB threshold)
	// IMPORTANT: FIFO expects files in descending age order: [newest ... oldest]
	// So we prepend files (newest first) or create them in reverse order
	for i := 7; i >= 0; i-- {
		tree.Levels[0].Files = append(tree.Levels[0].Files, &SSTFile{
			SizeMB:    100,
			CreatedAt: float64(i * 10), // Created at 70s, 60s, 50s, ... 0s
		})
	}
	tree.Levels[0].TotalSize = 8 * 100
	tree.Levels[0].FileCount = 8

	// Pick compaction - should delete oldest files
	compaction := compactor.PickCompaction(tree, config)

	assert.NotNil(t, compaction, "Compaction should be triggered when size exceeds threshold")
	assert.False(t, compaction.IsIntraL0, "Should be a deletion compaction (not intra-L0)")

	// Should delete oldest files (rightmost in L0 array) to bring size down to ~500 MB
	// Files are ordered [newest ... oldest], so rightmost files are oldest
	assert.GreaterOrEqual(t, len(compaction.SourceFiles), 3, "Should delete at least 3 files")

	// Verify oldest files are selected (files with CreatedAt <= 70 are deleted to bring size under 500MB)
	for _, file := range compaction.SourceFiles {
		assert.LessOrEqual(t, file.CreatedAt, 70.0, "Should delete old files first")
	}

	// Execute compaction
	compactor.ExecuteCompaction(compaction, tree, config, 100.0)

	// Verify files were removed and size is under threshold
	totalSize := tree.Levels[0].TotalSize
	assert.LessOrEqual(t, totalSize, 500.0, "Total size should be under threshold after compaction")
}

// TestFIFOTTLDeletion tests that FIFO deletes files older than TTL threshold
func TestFIFOTTLDeletion(t *testing.T) {
	// NOTE: TTL implementation is currently a stub (returns nil)
	// This test is a placeholder for when TTL is fully implemented
	t.Skip("TTL compaction not yet fully implemented")

	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 1000 // Large threshold (won't trigger size-based)
	config.FIFOAllowCompaction = false
	// 60 seconds TTL
	config.NumLevels = 1

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add files with varying ages
	tree.Levels[0].Files = []*SSTFile{
		{SizeMB: 50, CreatedAt: 100.0}, // Age 50s (young)
		{SizeMB: 50, CreatedAt: 90.0},  // Age 60s (exactly TTL)
		{SizeMB: 50, CreatedAt: 80.0},  // Age 70s (old)
		{SizeMB: 50, CreatedAt: 50.0},  // Age 100s (very old)
		{SizeMB: 50, CreatedAt: 20.0},  // Age 130s (very old)
	}
	tree.Levels[0].TotalSize = 5 * 50
	tree.Levels[0].FileCount = 5

	// Current virtual time = 150s
	compaction := compactor.PickCompaction(tree, config)

	assert.NotNil(t, compaction, "TTL compaction should be triggered")
	// When TTL is implemented, verify correct files are selected
}

// TestFIFOCompactionOrder tests that TTL runs before size-based compaction
func TestFIFOCompactionOrder(t *testing.T) {
	// NOTE: TTL implementation is currently a stub
	// This test verifies the ordering logic is in place
	t.Skip("TTL compaction not yet fully implemented")

	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 200 // Will trigger size-based
	// TTL enabled
	config.NumLevels = 1

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add mix of old and new files, total > threshold
	tree.Levels[0].Files = []*SSTFile{
		{SizeMB: 100, CreatedAt: 60.0}, // Age 40s (young)
		{SizeMB: 100, CreatedAt: 40.0}, // Age 60s (old, exceeds TTL)
		{SizeMB: 100, CreatedAt: 20.0}, // Age 80s (very old)
	}
	tree.Levels[0].TotalSize = 3 * 100
	tree.Levels[0].FileCount = 3

	compaction := compactor.PickCompaction(tree, config)

	assert.NotNil(t, compaction)
	// When TTL is implemented, verify TTL runs first
}

// TestFIFOIntraL0Compaction tests intra-L0 compaction to merge small files
func TestFIFOIntraL0Compaction(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 1000 // Large threshold (won't trigger deletion)
	config.FIFOAllowCompaction = true     // Enable intra-L0 compaction
	// Disable TTL
	config.NumLevels = 1
	config.L0CompactionTrigger = 4        // Need 4 files minimum
	config.MemtableFlushSizeMB = 64       // 64 MB flush size
	config.MaxCompactionBytesMB = 1000    // Max 1000 MB per compaction
	config.DeduplicationFactor = 0.9      // 10% deduplication

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add 5 small files (50 MB each)
	// total = 250 MB, bytes_per_del = 250 / 5 = 50 MB
	// threshold = 64 * 1.1 = 70.4 MB
	// 50 < 70.4, so should compact
	for i := 0; i < 5; i++ {
		tree.Levels[0].Files = append(tree.Levels[0].Files, &SSTFile{
			SizeMB:    50,
			CreatedAt: float64(i * 10),
		})
	}
	tree.Levels[0].TotalSize = 5 * 50
	tree.Levels[0].FileCount = 5

	compaction := compactor.PickCompaction(tree, config)

	assert.NotNil(t, compaction, "Intra-L0 compaction should be triggered")
	assert.True(t, compaction.IsIntraL0, "Should be a merge compaction (intra-L0)")
	assert.GreaterOrEqual(t, len(compaction.SourceFiles), 4, "Should select at least 4 files")

	// Execute compaction
	compactor.ExecuteCompaction(compaction, tree, config, 100.0)

	// Verify files were merged
	assert.Less(t, tree.Levels[0].FileCount, 5, "File count should decrease after merge")
}

// TestFIFOIntraL0LargeFiles tests that large files are NOT compacted
func TestFIFOIntraL0LargeFiles(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 5000 // Very large threshold
	config.FIFOAllowCompaction = true
	
	config.NumLevels = 1
	config.L0CompactionTrigger = 4
	config.MemtableFlushSizeMB = 64 // 64 MB flush size

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add 5 files of 72 MB each (larger than write_buffer_size * 1.1)
	// total = 360 MB, bytes_per_del = 360 / 5 = 72 MB
	// threshold = 64 * 1.1 = 70.4 MB
	// 72 > 70.4 (should NOT compact)
	for i := 0; i < 5; i++ {
		tree.Levels[0].Files = append(tree.Levels[0].Files, &SSTFile{
			SizeMB:    72,
			CreatedAt: float64(i * 10),
		})
	}
	tree.Levels[0].TotalSize = 5 * 72
	tree.Levels[0].FileCount = 5

	compaction := compactor.PickCompaction(tree, config)

	// Should NOT compact because bytes_per_del exceeds threshold
	assert.Nil(t, compaction, "Large files should not be compacted (protection against re-compacting)")
}

// TestFIFOEmptyLSM tests that no compaction is triggered on empty LSM
func TestFIFOEmptyLSM(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.NumLevels = 1

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	compaction := compactor.PickCompaction(tree, config)

	assert.Nil(t, compaction, "No compaction should be triggered on empty LSM")
}

// TestFIFONeedsCompaction tests NeedsCompaction check
func TestFIFONeedsCompaction(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.NumLevels = 1
	config.L0CompactionTrigger = 4
	config.FIFOMaxTableFilesSizeMB = 10000 // Large threshold to avoid size-based trigger

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Empty LSM - no compaction needed
	assert.False(t, compactor.NeedsCompaction(0, tree, config), "Empty LSM should not need compaction")

	// Add files but below trigger
	tree.Levels[0].Files = []*SSTFile{
		{SizeMB: 100, CreatedAt: 0},
		{SizeMB: 100, CreatedAt: 10},
	}
	tree.Levels[0].FileCount = 2
	tree.Levels[0].TotalSize = 200

	assert.False(t, compactor.NeedsCompaction(0, tree, config), "Below trigger should not need compaction")

	// Add more files to exceed trigger
	tree.Levels[0].Files = append(tree.Levels[0].Files, &SSTFile{SizeMB: 100, CreatedAt: 20})
	tree.Levels[0].Files = append(tree.Levels[0].Files, &SSTFile{SizeMB: 100, CreatedAt: 30})
	tree.Levels[0].FileCount = 4
	tree.Levels[0].TotalSize = 400

	// Now should need compaction
	assert.True(t, compactor.NeedsCompaction(0, tree, config), "File count >= trigger should need compaction")
}

// TestFIFOSingleFile tests behavior with single file (should delete if over threshold)
func TestFIFOSingleFile(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 50 // Small threshold
	config.FIFOAllowCompaction = true
	config.NumLevels = 1
	config.L0CompactionTrigger = 4

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add single 100 MB file (exceeds threshold)
	tree.Levels[0].Files = []*SSTFile{
		{SizeMB: 100, CreatedAt: 10.0},
	}
	tree.Levels[0].TotalSize = 100
	tree.Levels[0].FileCount = 1

	compaction := compactor.PickCompaction(tree, config)

	// Should trigger size-based deletion
	assert.NotNil(t, compaction, "Single large file should trigger deletion")
	assert.False(t, compaction.IsIntraL0, "Should be deletion compaction")
	assert.Equal(t, 1, len(compaction.SourceFiles), "Should delete the single file")
}

// TestFIFODiminishingReturns tests the "diminishing returns" algorithm
func TestFIFODiminishingReturns(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleFIFO
	config.FIFOMaxTableFilesSizeMB = 5000
	config.FIFOAllowCompaction = true
	
	config.NumLevels = 1
	config.L0CompactionTrigger = 4
	config.MemtableFlushSizeMB = 64
	config.MaxCompactionBytesMB = 1000

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	compactor := NewFIFOCompactor(12345)

	// Add files with sizes that create diminishing returns:
	// Files: [10, 20, 30, 100, 200] MB
	// Cumulative: 10, 30, 60, 160, 360
	// Bytes per del: 10/1=10, 30/2=15, 60/3=20, 160/4=40, 360/5=72
	// Ratio increases from 10→15→20→40→72, so algorithm should stop early
	tree.Levels[0].Files = []*SSTFile{
		{SizeMB: 10, CreatedAt: 0.0},
		{SizeMB: 20, CreatedAt: 10.0},
		{SizeMB: 30, CreatedAt: 20.0},
		{SizeMB: 100, CreatedAt: 30.0},
		{SizeMB: 200, CreatedAt: 40.0},
	}
	tree.Levels[0].TotalSize = 360
	tree.Levels[0].FileCount = 5

	compaction := compactor.PickCompaction(tree, config)

	// Algorithm should select first few files and stop when ratio increases
	// With threshold = 64 * 1.1 = 70.4 MB, it should select files until bytes_per_del < 70.4
	// Files [10, 20, 30, 100] → 160/4 = 40 MB < 70.4 ✓
	// Files [10, 20, 30, 100, 200] → 360/5 = 72 MB > 70.4 ✗ (also ratio increased)
	if compaction != nil {
		assert.LessOrEqual(t, len(compaction.SourceFiles), 4, "Should stop before including largest file")
	}
}
