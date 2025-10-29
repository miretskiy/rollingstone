package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPopulateInitialLSM(t *testing.T) {
	config := DefaultConfig()
	config.InitialLSMSizeMB = 10000 // 10 GB
	config.NumLevels = 7

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Reset triggers population
	sim.Reset()

	// Verify total size matches requested size (not double)
	totalSize := sim.lsm.TotalSizeMB
	require.InDelta(t, 10000.0, totalSize, 100.0, "Total LSM size should be ~10GB, got %.1f MB", totalSize)

	// Verify data is distributed across levels
	levelSizes := make(map[int]float64)
	for i := 1; i < len(sim.lsm.Levels); i++ {
		levelSizes[i] = sim.lsm.Levels[i].TotalSize
	}

	// L0 should be empty (we only populate L1+)
	require.Equal(t, 0.0, sim.lsm.Levels[0].TotalSize, "L0 should be empty")

	// At least some levels should have data
	nonEmptyLevels := 0
	for i := 1; i < len(sim.lsm.Levels); i++ {
		if sim.lsm.Levels[i].TotalSize > 0 {
			nonEmptyLevels++
		}
	}
	require.Greater(t, nonEmptyLevels, 0, "At least one level should have data")

	// Sum of all level sizes should equal total size
	sumLevelSizes := 0.0
	for i := 0; i < len(sim.lsm.Levels); i++ {
		sumLevelSizes += sim.lsm.Levels[i].TotalSize
	}
	require.InDelta(t, totalSize, sumLevelSizes, 0.1, "Sum of level sizes should equal total size")
}

func TestPopulateLevel(t *testing.T) {
	config := DefaultConfig()
	config.TargetFileSizeMB = 64
	config.TargetFileSizeMultiplier = 1

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Populate L1 with 640 MB (should create 10 files of 64 MB each)
	targetSize := 640.0
	level := 1

	beforeTotalSize := sim.lsm.TotalSizeMB
	beforeFileCount := sim.lsm.Levels[level].FileCount

	sim.populateLevel(level, targetSize)

	afterTotalSize := sim.lsm.TotalSizeMB
	afterFileCount := sim.lsm.Levels[level].FileCount

	// Verify total size increased by exactly targetSize
	require.InDelta(t, beforeTotalSize+targetSize, afterTotalSize, 0.1,
		"Total size should increase by %.1f MB, before=%.1f, after=%.1f",
		targetSize, beforeTotalSize, afterTotalSize)

	// Verify level size equals targetSize
	require.InDelta(t, targetSize, sim.lsm.Levels[level].TotalSize, 0.1,
		"Level %d size should be %.1f MB, got %.1f MB",
		level, targetSize, sim.lsm.Levels[level].TotalSize)

	// Verify files were created (640/64 = 10 files)
	expectedFiles := 10
	actualFiles := afterFileCount - beforeFileCount
	require.Equal(t, expectedFiles, actualFiles,
		"Should create %d files of 64MB each, got %d files",
		expectedFiles, actualFiles)

	// Verify each file has correct size
	for i := beforeFileCount; i < afterFileCount; i++ {
		file := sim.lsm.Levels[level].Files[i]
		require.Equal(t, 64.0, file.SizeMB, "File %d should be 64MB, got %.1f MB", i, file.SizeMB)
	}
}

func TestPopulateLevelWithPartialFile(t *testing.T) {
	config := DefaultConfig()
	config.TargetFileSizeMB = 64
	config.TargetFileSizeMultiplier = 1

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	// Populate L1 with 200 MB (should create 3 files: 64, 64, 72)
	targetSize := 200.0
	level := 1

	sim.populateLevel(level, targetSize)

	// Verify total matches
	require.InDelta(t, targetSize, sim.lsm.Levels[level].TotalSize, 0.1,
		"Level size should be %.1f MB, got %.1f MB",
		targetSize, sim.lsm.Levels[level].TotalSize)

	// Verify 4 files created (64 + 64 + 64 + 8 = 200)
	require.Equal(t, 4, sim.lsm.Levels[level].FileCount, "Should create 4 files")

	// Verify last file is partial
	lastFile := sim.lsm.Levels[level].Files[sim.lsm.Levels[level].FileCount-1]
	require.Equal(t, 8.0, lastFile.SizeMB, "Last file should be 8MB (partial), got %.1f MB", lastFile.SizeMB)
}

func TestPopulateInitialLSM_ZeroSize(t *testing.T) {
	config := DefaultConfig()
	config.InitialLSMSizeMB = 0 // No initial data

	sim, err := NewSimulator(config)
	require.NoError(t, err)

	sim.Reset()

	// Verify LSM is empty
	require.Equal(t, 0.0, sim.lsm.TotalSizeMB, "LSM should be empty when InitialLSMSizeMB=0")

	for i := 0; i < len(sim.lsm.Levels); i++ {
		require.Equal(t, 0.0, sim.lsm.Levels[i].TotalSize, "Level %d should be empty", i)
		require.Equal(t, 0, sim.lsm.Levels[i].FileCount, "Level %d should have no files", i)
	}
}
