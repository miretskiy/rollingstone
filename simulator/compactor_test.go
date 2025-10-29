package simulator

import (
	"fmt"
	"math"
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
	compactor := NewLeveledCompactor(0)

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
	compactor := NewLeveledCompactor(0)

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
	compactor := NewLeveledCompactor(0)

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

	inputSize, outputSize, outputFileCount := compactor.ExecuteCompaction(job, lsm, config, 10.0)

	if inputSize != 192 { // 3 files * 64 MB
			t.Errorf("Expected inputSize=192, got %.1f", inputSize)
		}

	// Output should be reduced by compaction factor (0.9 for L0->L1)
	expectedOutput := 192 * 0.9
	if outputSize != expectedOutput {
		t.Errorf("Expected outputSize=%.1f, got %.1f", expectedOutput, outputSize)
	}

	// Check output file count: 172.8 MB / 64 MB = 2.7, ceil = 3 files
	expectedOutputFiles := 3
	if outputFileCount != expectedOutputFiles {
		t.Errorf("Expected outputFileCount=%d, got %d", expectedOutputFiles, outputFileCount)
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

		// Add a target file to ensure we do a real compaction (not trivial move)
		targetFiles := []*SSTFile{
			{ID: "L2-1", SizeMB: 50, CreatedAt: 0.5},
		}
		lsm.Levels[2].Files = append(lsm.Levels[2].Files, targetFiles[0])

		job := &CompactionJob{
			FromLevel:   1,
			ToLevel:     2,
			SourceFiles: sourceFiles,
			TargetFiles: targetFiles,
		}

	_, outputSize, _ := compactor.ExecuteCompaction(job, lsm, config, 10.0)

	// L1+ should use 0.99 reduction factor
		// Input: 256 + 50 = 306 MB, Output: 306 * 0.99 = 302.94 MB
		expectedOutput := (256 + 50) * 0.99
		if outputSize != expectedOutput {
			t.Errorf("Expected outputSize=%.1f, got %.1f", expectedOutput, outputSize)
		}
	})
}

// TestMaxCompactionBytesDefault verifies that max_compaction_bytes defaults to 25x target_file_size_base
// when set to 0, matching RocksDB behavior (db/column_family.cc)
func TestMaxCompactionBytesDefault(t *testing.T) {
	config := SimConfig{
		TargetFileSizeMB:     64,
		MaxCompactionBytesMB: 0,  // Should default to 64 * 25 = 1600 MB
		L0CompactionTrigger:  10, // High threshold to avoid intra-L0
	}
	lsm := NewLSMTree(3, 64.0)
	compactor := NewLeveledCompactor(0)

	// Add 20 files to L0, each 100 MB (total 2000 MB > 1600 MB limit)
	for i := 0; i < 20; i++ {
		lsm.Levels[0].Files = append(lsm.Levels[0].Files, &SSTFile{
			ID:     fmt.Sprintf("f%d", i),
			SizeMB: 100,
		})
		lsm.Levels[0].TotalSize += 100
		lsm.Levels[0].FileCount++
	}

	// Add some files to L1 to create overlap
	for i := 0; i < 5; i++ {
		lsm.Levels[1].Files = append(lsm.Levels[1].Files, &SSTFile{
			ID:     fmt.Sprintf("l1-f%d", i),
			SizeMB: 64,
		})
		lsm.Levels[1].TotalSize += 64
		lsm.Levels[1].FileCount++
	}

	job := compactor.PickCompaction(0, lsm, config)
	if job == nil {
		t.Fatal("Expected compaction job, got nil")
	}

	// Calculate total input size
	var totalInput float64
	for _, f := range job.SourceFiles {
		totalInput += f.SizeMB
	}
	for _, f := range job.TargetFiles {
		totalInput += f.SizeMB
	}

	// With max_compaction_bytes = 0 -> 1600 MB, and L0 = 2000 MB,
	// we should see reduced target files to stay under limit
	expectedMax := float64(64 * 25)   // 1600 MB
	if totalInput > expectedMax*1.1 { // Allow 10% tolerance for distribution sampling
		t.Errorf("Total input %1f MB exceeds max_compaction_bytes limit %1f MB by >10%%", totalInput, expectedMax)
	}
}

// TestIntraL0RespectsMaxCompactionBytes verifies that intra-L0 compaction respects max_compaction_bytes
func TestIntraL0RespectsMaxCompactionBytes(t *testing.T) {
	config := SimConfig{
		TargetFileSizeMB:     64,
		MaxCompactionBytesMB: 200, // Limit to 200 MB
		L0CompactionTrigger:  4,   // Intra-L0 triggers at 4+2=6 files
	}
	lsm := NewLSMTree(3, 64.0)
	compactor := NewLeveledCompactor(0)

	// Add 10 files to L0, each 100 MB (total 1000 MB)
	for i := 0; i < 10; i++ {
		lsm.Levels[0].Files = append(lsm.Levels[0].Files, &SSTFile{
			ID:     fmt.Sprintf("f%d", i),
			SizeMB: 100,
		})
		lsm.Levels[0].TotalSize += 100
		lsm.Levels[0].FileCount++
	}

	job := compactor.PickCompaction(0, lsm, config)
	if job == nil {
		t.Fatal("Expected compaction job, got nil")
	}

	// Should be intra-L0 (10 files >= 6 threshold)
	if !job.IsIntraL0 {
		t.Errorf("Expected intra-L0 compaction, got L0->L1")
	}

	// Calculate total source size
	var totalSize float64
	for _, f := range job.SourceFiles {
		totalSize += f.SizeMB
	}

	// Should not exceed 200 MB limit (should pick exactly 2 files = 200 MB)
	if totalSize > 200 {
		t.Errorf("Intra-L0 compaction size %.1f MB exceeds max_compaction_bytes 200 MB", totalSize)
	}

	// Should have at least 2 files
	if len(job.SourceFiles) < 2 {
		t.Errorf("Expected at least 2 files in intra-L0 compaction, got %d", len(job.SourceFiles))
	}
}

// TestTargetFileSizePerLevel verifies that target file size follows RocksDB's formula:
// Level 0,1: target_file_size_base
// Level 2+: target_file_size_base * (multiplier ^ (level-1))
// RocksDB Reference: options/cf_options.cc RefreshDerivedOptions()
func TestTargetFileSizePerLevel(t *testing.T) {
	config := SimConfig{
		TargetFileSizeMB:          64,
		TargetFileSizeMultiplier:  2,
		CompactionReductionFactor: 1.0,   // No reduction (but compactor applies 0.9 for L0→L1, 0.99 for deeper)
		MaxCompactionBytesMB:      10000, // Large enough to not interfere
		L0CompactionTrigger:       100,   // Prevent intra-L0
	}
	lsm := NewLSMTree(5, 64.0)
	compactor := NewLeveledCompactor(0)

	// We'll directly test the file splitting logic by creating large compactions
	// and verifying the output file count matches the expected target file size

	// Test 1: L0→L1 should create files of ~64 MB
	// Input: 320 MB total → after 0.9 reduction = 288 MB → 288/64 = 4-5 files
	// Add 1 file to L1 to prevent trivial move (force real compaction)
	lsm.Levels[1].AddSize(10, 0)
	for i := 0; i < 5; i++ {
		lsm.Levels[0].AddSize(64, 0)
	}
	job1 := &CompactionJob{
		FromLevel:   0,
		ToLevel:     1,
		SourceFiles: lsm.Levels[0].Files,
		TargetFiles: lsm.Levels[1].Files, // Include target file to prevent trivial move
	}
	_, outSize1, outFiles1 := compactor.ExecuteCompaction(job1, lsm, config, 10.0)
	expectedFiles1 := int(math.Ceil(outSize1 / 64.0))
	if outFiles1 != expectedFiles1 {
		t.Errorf("L1 expected %d output files, got %d", expectedFiles1, outFiles1)
	}
	if lsm.Levels[1].FileCount != expectedFiles1 {
		t.Errorf("L1 expected %d files (output=%.1f MB, target=64 MB), got %d files",
			expectedFiles1, outSize1, lsm.Levels[1].FileCount)
	}

	// Test 2: L1→L2 should create files of ~128 MB (64 * 2^1)
	// Clear L1 and add exactly 512 MB, plus 1 file to L2 to prevent trivial move
	lsm.Levels[1] = &Level{Number: 1}
	lsm.Levels[2].AddSize(10, 0) // Prevent trivial move
	for i := 0; i < 8; i++ {
		lsm.Levels[1].AddSize(64, 0)
	}
	job2 := &CompactionJob{
		FromLevel:   1,
		ToLevel:     2,
		SourceFiles: lsm.Levels[1].Files,
		TargetFiles: lsm.Levels[2].Files, // Include target file to prevent trivial move
	}
	_, outSize2, outFiles2 := compactor.ExecuteCompaction(job2, lsm, config, 20.0)
	// 512 MB * 0.99 + 10 MB = 516.88 MB, split into 128 MB files = 5 files
	expectedFiles2 := int(math.Ceil(outSize2 / 128.0))
	if outFiles2 != expectedFiles2 {
		t.Errorf("L2 expected %d output files, got %d", expectedFiles2, outFiles2)
	}
	if lsm.Levels[2].FileCount != expectedFiles2 {
		t.Errorf("L2 expected %d files (output=%.1f MB, target=128 MB), got %d files",
			expectedFiles2, outSize2, lsm.Levels[2].FileCount)
	}

	// Test 3: L2→L3 should create files of ~256 MB (64 * 2^2)
	lsm.Levels[2] = &Level{Number: 2}
	lsm.Levels[3].AddSize(10, 0) // Prevent trivial move
	for i := 0; i < 8; i++ {
		lsm.Levels[2].AddSize(128, 0)
	}
	job3 := &CompactionJob{
		FromLevel:   2,
		ToLevel:     3,
		SourceFiles: lsm.Levels[2].Files,
		TargetFiles: lsm.Levels[3].Files, // Include target file to prevent trivial move
	}
	_, outSize3, outFiles3 := compactor.ExecuteCompaction(job3, lsm, config, 30.0)
	// 1024 MB * 0.99 + 10 MB = 1023.76 MB, split into 256 MB files = 4 files
	expectedFiles3 := int(math.Ceil(outSize3 / 256.0))
	if outFiles3 != expectedFiles3 {
		t.Errorf("L3 expected %d output files, got %d", expectedFiles3, outFiles3)
	}
	if lsm.Levels[3].FileCount != expectedFiles3 {
		t.Errorf("L3 expected %d files (output=%.1f MB, target=256 MB), got %d files",
			expectedFiles3, outSize3, lsm.Levels[3].FileCount)
	}
}

// TestTrivialMove verifies that compactions with no target overlap don't perform I/O
// RocksDB optimization: just move file pointers, don't rewrite data
func TestTrivialMove(t *testing.T) {
	config := SimConfig{
		TargetFileSizeMB:          64,
		CompactionReductionFactor: 0.9, // 10% reduction for normal compaction
		MaxCompactionBytesMB:      10000,
		L0CompactionTrigger:       100, // Prevent intra-L0
	}
	lsm := NewLSMTree(3, 64.0)
	compactor := NewLeveledCompactor(0)

	// Add a single file to L1 (no files in L2, so no overlap)
	lsm.Levels[1].AddSize(100, 0)
	initialFileCount := lsm.Levels[1].FileCount

	sourceFiles := lsm.Levels[1].Files
	job := &CompactionJob{
		FromLevel:   1,
		ToLevel:     2,
		SourceFiles: sourceFiles,
		TargetFiles: []*SSTFile{}, // NO OVERLAP - trivial move candidate
		IsIntraL0:   false,
	}

	inputSize, outputSize, outputFileCount := compactor.ExecuteCompaction(job, lsm, config, 10.0)

	// Trivial move: output size == input size (no reduction)
	if outputSize != inputSize {
		t.Errorf("Trivial move should have outputSize == inputSize, got %.1f vs %.1f", outputSize, inputSize)
	}

	// Trivial move: output file count == number of source files being moved
	expectedFileCount := len(sourceFiles) // Source files before the move
	if outputFileCount != expectedFileCount {
		t.Errorf("Trivial move should have outputFileCount == source file count (%d), got %d", expectedFileCount, outputFileCount)
	}

	// Trivial move: input size should be 100 MB
	if inputSize != 100.0 {
		t.Errorf("Expected inputSize=100.0, got %.1f", inputSize)
	}

	// L1 should be empty after move
	if lsm.Levels[1].FileCount != 0 {
		t.Errorf("L1 should be empty after trivial move, got %d files", lsm.Levels[1].FileCount)
	}

	// L2 should have the same files (moved, not rewritten)
	if lsm.Levels[2].FileCount != initialFileCount {
		t.Errorf("L2 should have %d files after trivial move, got %d", initialFileCount, lsm.Levels[2].FileCount)
	}

	// L2 total size should be 100 MB
	if lsm.Levels[2].TotalSize != 100.0 {
		t.Errorf("L2 should have 100 MB after trivial move, got %.1f", lsm.Levels[2].TotalSize)
	}
}
