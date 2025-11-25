package simulator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmptyLbaseThresholdBug demonstrates that our threshold logic
// (2.0 for empty target, 1.5 for < 3 files) is WRONG.
//
// RocksDB Reference: compaction_picker_level.cc:606-613
// RocksDB DOES compact to empty base level - it just skips trivial move.
//
// RocksDB C++ (lines 606-613):
//   ```cpp
//   if (start_level_ == 0 && mutable_cf_options_.compression_per_level.empty() &&
//       !vstorage_->LevelFiles(output_level_).empty() &&  // <-- Skips trivial move if empty
//       ioptions_.db_paths.size() <= 1) {
//     // ...
//     // We skip the case where output level is empty, since in this case, at
//     // least the oldest file would qualify for trivial move, and this would
//     // be a surprising behavior with few benefits.
//   ```
//
// RocksDB has NO threshold check. It compacts when score > 1.0 regardless
// of whether target level is empty.
//
// Our Bug: We require score > 2.0 when target is empty, blocking compactions.
func TestEmptyLbaseThresholdBug(t *testing.T) {
	config := DefaultConfig()
	config.L0CompactionTrigger = 4
	config.MaxBytesForLevelBaseMB = 256
	config.LevelMultiplier = 10

	lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Add 7 L0 files → score = 7/4 = 1.75
	for i := 0; i < 7; i++ {
		lsm.Levels[0].Files = append(lsm.Levels[0].Files, &SSTFile{
			ID:     fmt.Sprintf("file-%d", i),
			SizeMB: 64.0,
		})
	}
	lsm.Levels[0].FileCount = 7
	lsm.Levels[0].TotalSize = 7 * 64.0

	// L1 is EMPTY
	require.Equal(t, 0, lsm.Levels[1].FileCount, "L1 should be empty")

	// Calculate score
	score := lsm.calculateCompactionScore(0, config, 0)
	require.Equal(t, 1.75, score, "L0 score should be 1.75 (7 files / 4 trigger)")

	// RocksDB behavior: score > 1.0 → compact (regardless of empty L1)
	// Our bug: threshold = 2.0 for empty L1, so 1.75 < 2.0 → NO compaction!

	compactor := NewLeveledCompactor(0)
	job := compactor.PickCompaction(lsm, config)

	// FIX VERIFIED: After removing 2.0 threshold, compaction is now picked
	// RocksDB would compact when score > 1.0, and now so do we
	require.NotNil(t, job, "Should pick compaction when score > 1.0 even if L1 empty")
	require.Equal(t, 0, job.FromLevel, "Should be L0")

	// Note: May pick intra-L0 instead of L0→L1 due to size-based preference
	// (L1 is empty = 0 MB, so intra-L0 is preferred to avoid write-amp)
	// The key fix is that compaction is NO LONGER BLOCKED by artificial threshold
	t.Logf("✓ FIX VERIFIED: Compaction picked when score=1.75 (was blocked before)")
	t.Logf("  Compaction type: L%d → L%d", job.FromLevel, job.ToLevel)
	if job.ToLevel == 0 {
		t.Logf("  Note: Picked intra-L0 due to empty L1 (size-based preference)")
	}
}

// TestEmptyLbaseWithHigherScore verifies compaction DOES occur when
// score exceeds our artificial 2.0 threshold.
func TestEmptyLbaseWithHigherScore(t *testing.T) {
	config := DefaultConfig()
	config.L0CompactionTrigger = 4
	config.MaxBytesForLevelBaseMB = 256
	config.LevelMultiplier = 10

	lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Add 9 L0 files → score = 9/4 = 2.25 > 2.0
	for i := 0; i < 9; i++ {
		lsm.Levels[0].Files = append(lsm.Levels[0].Files, &SSTFile{
			ID:     fmt.Sprintf("file-%d", i),
			SizeMB: 64.0,
		})
	}
	lsm.Levels[0].FileCount = 9
	lsm.Levels[0].TotalSize = 9 * 64.0

	// L1 is EMPTY
	require.Equal(t, 0, lsm.Levels[1].FileCount, "L1 should be empty")

	score := lsm.calculateCompactionScore(0, config, 0)
	require.Equal(t, 2.25, score, "L0 score should be 2.25")

	compactor := NewLeveledCompactor(0)
	job := compactor.PickCompaction(lsm, config)

	// Should pick compaction since 2.25 > 2.0 threshold
	require.NotNil(t, job, "Should pick compaction when score > 2.0")
	require.Equal(t, 0, job.FromLevel, "Should be L0")

	// Note: May target different level with dynamic base level calculation
	t.Logf("Compaction picked: L%d → L%d", job.FromLevel, job.ToLevel)
}
