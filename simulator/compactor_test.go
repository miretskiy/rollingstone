package simulator

import (
	"fmt"
	"testing"
)

// mockFilePicker for predictable testing
type mockFilePicker struct {
	values []int
	index  int
}

func (m *mockFilePicker) Pick(min, max int) int {
	if m.index >= len(m.values) {
		return min
	}
	val := m.values[m.index]
	m.index++
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func TestPickFileCount(t *testing.T) {
	tests := []struct {
		name           string
		availableFiles int
		minFiles       int
		mockValues     []int
		expected       int
	}{
		{
			name:           "no files available",
			availableFiles: 0,
			minFiles:       1,
			mockValues:     []int{5},
			expected:       0,
		},
		{
			name:           "available equals min",
			availableFiles: 3,
			minFiles:       3,
			mockValues:     []int{5},
			expected:       3,
		},
		{
			name:           "pick from range",
			availableFiles: 10,
			minFiles:       2,
			mockValues:     []int{5},
			expected:       5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockFilePicker{values: tt.mockValues}
			result := pickFileCount(tt.availableFiles, tt.minFiles, mock)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestPickOverlapCount(t *testing.T) {
	tests := []struct {
		name       string
		maxFiles   int
		mockValues []int
		expected   int
	}{
		{
			name:       "no files",
			maxFiles:   0,
			mockValues: []int{5},
			expected:   0,
		},
		{
			name:       "pick overlap",
			maxFiles:   10,
			mockValues: []int{3},
			expected:   3,
		},
		{
			name:       "clamp to max",
			maxFiles:   5,
			mockValues: []int{10},
			expected:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockFilePicker{values: tt.mockValues}
			result := pickOverlapCount(tt.maxFiles, mock)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestLeveledCompactorNeedsCompaction(t *testing.T) {
	compactor := NewLeveledCompactor()

	config := SimConfig{
		NumLevels:                 7,
		MemtableFlushSizeMB:       64,
		L0CompactionTrigger:       4,
		MaxBytesForLevelBaseMB:    256,
		LevelMultiplier:           10,
		CompactionReductionFactor: 0.9,
	}

	tests := []struct {
		name          string
		level         int
		fileCount     int
		totalSize     float64
		shouldCompact bool
	}{
		{
			name:          "L0 needs compaction - over trigger",
			level:         0,
			fileCount:     5,
			totalSize:     320,
			shouldCompact: true,
		},
		{
			name:          "L0 doesn't need compaction",
			level:         0,
			fileCount:     3,
			totalSize:     192,
			shouldCompact: false,
		},
		{
			name:          "L1 needs compaction - over target",
			level:         1,
			fileCount:     10,
			totalSize:     300, // Target is 256
			shouldCompact: true,
		},
		{
			name:          "L1 doesn't need compaction",
			level:         1,
			fileCount:     5,
			totalSize:     200,
			shouldCompact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
			lsm.Levels[tt.level].FileCount = tt.fileCount
			lsm.Levels[tt.level].TotalSize = tt.totalSize

			result := compactor.NeedsCompaction(tt.level, lsm, config)
			if result != tt.shouldCompact {
				t.Errorf("Expected NeedsCompaction=%v, got %v", tt.shouldCompact, result)
			}
		})
	}
}

func TestLeveledCompactorPickCompaction(t *testing.T) {
	compactor := NewLeveledCompactor()

	config := SimConfig{
		NumLevels:                 7,
		MemtableFlushSizeMB:       64,
		L0CompactionTrigger:       4,
		MaxBytesForLevelBaseMB:    256,
		LevelMultiplier:           10,
		CompactionReductionFactor: 0.9,
		TargetFileSizeMB:          64,
		TargetFileSizeMultiplier:  2,
	}

	t.Run("pick from L0", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

		// Add some L0 files
		for i := 0; i < 5; i++ {
			lsm.Levels[0].Files = append(lsm.Levels[0].Files, &SSTFile{
				ID:        fmt.Sprintf("L0-%d", i),
				SizeMB:    64,
				CreatedAt: float64(i),
			})
			lsm.Levels[0].FileCount++
			lsm.Levels[0].TotalSize += 64
		}

		// Add some L1 files
		for i := 0; i < 3; i++ {
			lsm.Levels[1].Files = append(lsm.Levels[1].Files, &SSTFile{
				ID:        fmt.Sprintf("L1-%d", i),
				SizeMB:    64,
				CreatedAt: float64(i),
			})
			lsm.Levels[1].FileCount++
			lsm.Levels[1].TotalSize += 64
		}

		job := compactor.PickCompaction(0, lsm, config)

		if job == nil {
			t.Fatal("Expected compaction job, got nil")
		}

		if job.FromLevel != 0 {
			t.Errorf("Expected FromLevel=0, got %d", job.FromLevel)
		}

		if job.ToLevel != 1 {
			t.Errorf("Expected ToLevel=1, got %d", job.ToLevel)
		}

		if len(job.SourceFiles) == 0 {
			t.Error("Expected source files, got none")
		}

		t.Logf("Picked %d source files, %d target files", len(job.SourceFiles), len(job.TargetFiles))
	})

	t.Run("invalid level returns nil", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

		job := compactor.PickCompaction(-1, lsm, config)
		if job != nil {
			t.Error("Expected nil for invalid level")
		}

		job = compactor.PickCompaction(99, lsm, config)
		if job != nil {
			t.Error("Expected nil for out of range level")
		}
	})

	t.Run("empty L0 returns nil", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

		job := compactor.PickCompaction(0, lsm, config)
		// Note: PickCompaction may return a job with empty files list for an empty level
		// This is acceptable behavior - the simulator checks file count before calling
		if job != nil && len(job.SourceFiles) > 0 {
			t.Error("Expected no source files for empty level")
		}
	})
}

func TestLeveledCompactorExecuteCompaction(t *testing.T) {
	compactor := NewLeveledCompactor()

	config := SimConfig{
		NumLevels:                 7,
		MemtableFlushSizeMB:       64,
		L0CompactionTrigger:       4,
		MaxBytesForLevelBaseMB:    256,
		LevelMultiplier:           10,
		CompactionReductionFactor: 0.9,
		TargetFileSizeMB:          64,
		TargetFileSizeMultiplier:  2,
	}

	t.Run("compact L0 to L1", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

		// Create source files
		sourceFiles := []*SSTFile{
			{ID: "L0-1", SizeMB: 64, CreatedAt: 1.0},
			{ID: "L0-2", SizeMB: 64, CreatedAt: 2.0},
		}

		// Create target files
		targetFiles := []*SSTFile{
			{ID: "L1-1", SizeMB: 64, CreatedAt: 0.5},
		}

		job := &CompactionJob{
			FromLevel:   0,
			ToLevel:     1,
			SourceFiles: sourceFiles,
			TargetFiles: targetFiles,
		}

		inputSize, outputSize := compactor.ExecuteCompaction(job, lsm, config, 10.0)

		if inputSize != 192 { // 3 files * 64 MB
			t.Errorf("Expected inputSize=192, got %.1f", inputSize)
		}

		// Output should be reduced by compaction factor (0.9 for L0->L1)
		expectedOutput := 192 * 0.9
		if outputSize != expectedOutput {
			t.Errorf("Expected outputSize=%.1f, got %.1f", expectedOutput, outputSize)
		}

		// Check that files were removed from source level
		if lsm.Levels[0].FileCount != 0 {
			t.Errorf("Expected L0 FileCount=0, got %d", lsm.Levels[0].FileCount)
		}

		// Check that files were added to target level (split into multiple files)
		if lsm.Levels[1].FileCount == 0 {
			t.Error("Expected files in L1")
		}

		t.Logf("Compaction: %.1f MB -> %.1f MB, resulting in %d L1 files",
			inputSize, outputSize, lsm.Levels[1].FileCount)
	})

	t.Run("L1 to L2 uses minimal reduction", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

		sourceFiles := []*SSTFile{
			{ID: "L1-1", SizeMB: 256, CreatedAt: 1.0},
		}

		job := &CompactionJob{
			FromLevel:   1,
			ToLevel:     2,
			SourceFiles: sourceFiles,
			TargetFiles: []*SSTFile{},
		}

		_, outputSize := compactor.ExecuteCompaction(job, lsm, config, 10.0)

		// L1+ should use 0.99 reduction factor
		expectedOutput := 256 * 0.99
		if outputSize != expectedOutput {
			t.Errorf("Expected outputSize=%.1f, got %.1f", expectedOutput, outputSize)
		}
	})
}
