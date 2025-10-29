package simulator

import (
	"testing"
)

func TestNewLevel(t *testing.T) {
	level := NewLevel(3)

	if level.Number != 3 {
		t.Errorf("Expected level number 3, got %d", level.Number)
	}

	if level.FileCount != 0 {
		t.Errorf("Expected file count 0, got %d", level.FileCount)
	}

	if level.TotalSize != 0 {
		t.Errorf("Expected total size 0, got %.1f", level.TotalSize)
	}

	if len(level.Files) != 0 {
		t.Errorf("Expected empty files slice, got length %d", len(level.Files))
	}
}

func TestLevelAddFile(t *testing.T) {
	level := NewLevel(0)

	file := &SSTFile{
		ID:        "test-1",
		SizeMB:    64.5,
		CreatedAt: 10.0,
	}

	level.AddFile(file)

	if level.FileCount != 1 {
		t.Errorf("Expected file count 1, got %d", level.FileCount)
	}

	if level.TotalSize != 64.5 {
		t.Errorf("Expected total size 64.5, got %.1f", level.TotalSize)
	}

	if len(level.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(level.Files))
	}

	if level.Files[0].ID != "test-1" {
		t.Errorf("Expected file ID 'test-1', got '%s'", level.Files[0].ID)
	}
}

func TestLevelAddSize(t *testing.T) {
	level := NewLevel(1)

	level.AddSize(128.0, 20.0)

	if level.FileCount != 1 {
		t.Errorf("Expected file count 1, got %d", level.FileCount)
	}

	if level.TotalSize != 128.0 {
		t.Errorf("Expected total size 128.0, got %.1f", level.TotalSize)
	}

	file := level.Files[0]
	if file.SizeMB != 128.0 {
		t.Errorf("Expected file size 128.0, got %.1f", file.SizeMB)
	}

	if file.CreatedAt != 20.0 {
		t.Errorf("Expected created at 20.0, got %.1f", file.CreatedAt)
	}
}

func TestLevelRemoveFiles(t *testing.T) {
	level := NewLevel(0)

	files := []*SSTFile{
		{ID: "file-1", SizeMB: 64.0, CreatedAt: 1.0},
		{ID: "file-2", SizeMB: 32.0, CreatedAt: 2.0},
		{ID: "file-3", SizeMB: 48.0, CreatedAt: 3.0},
	}

	for _, f := range files {
		level.AddFile(f)
	}

	if level.FileCount != 3 || level.TotalSize != 144.0 {
		t.Fatalf("Setup failed: expected 3 files, 144.0 MB, got %d files, %.1f MB",
			level.FileCount, level.TotalSize)
	}

	// Remove file-2
	level.RemoveFiles([]*SSTFile{files[1]})

	if level.FileCount != 2 {
		t.Errorf("Expected file count 2, got %d", level.FileCount)
	}

	if level.TotalSize != 112.0 {
		t.Errorf("Expected total size 112.0, got %.1f", level.TotalSize)
	}

	// Verify file-2 is gone
	for _, f := range level.Files {
		if f.ID == "file-2" {
			t.Error("file-2 should have been removed")
		}
	}

	// Verify file-1 and file-3 remain
	foundFile1, foundFile3 := false, false
	for _, f := range level.Files {
		if f.ID == "file-1" {
			foundFile1 = true
		}
		if f.ID == "file-3" {
			foundFile3 = true
		}
	}

	if !foundFile1 || !foundFile3 {
		t.Error("file-1 and file-3 should remain")
	}
}

func TestSSTFileAgeSeconds(t *testing.T) {
	file := &SSTFile{
		ID:        "test",
		SizeMB:    64.0,
		CreatedAt: 100.0,
	}

	age := file.AgeSeconds(150.0)
	if age != 50.0 {
		t.Errorf("Expected age 50.0, got %.1f", age)
	}

	// Age at creation time
	age = file.AgeSeconds(100.0)
	if age != 0.0 {
		t.Errorf("Expected age 0.0, got %.1f", age)
	}
}

func TestNewLSMTree(t *testing.T) {
	tree := NewLSMTree(7, 64.0)

	if len(tree.Levels) != 7 {
		t.Errorf("Expected 7 levels, got %d", len(tree.Levels))
	}

	if tree.MemtableMaxSize != 64.0 {
		t.Errorf("Expected memtable max size 64.0, got %.1f", tree.MemtableMaxSize)
	}

	if tree.MemtableCurrentSize != 0.0 {
		t.Errorf("Expected initial memtable size 0.0, got %.1f", tree.MemtableCurrentSize)
	}

	if tree.TotalSizeMB != 0.0 {
		t.Errorf("Expected total size 0.0, got %.1f", tree.TotalSizeMB)
	}

	// Verify all levels are initialized correctly
	for i, level := range tree.Levels {
		if level.Number != i {
			t.Errorf("Level %d has wrong number %d", i, level.Number)
		}
	}
}

func TestLSMTreeAddWrite(t *testing.T) {
	tree := NewLSMTree(3, 64.0)

	tree.AddWrite(10.0, 5.0)

	if tree.MemtableCurrentSize != 10.0 {
		t.Errorf("Expected memtable size 10.0, got %.1f", tree.MemtableCurrentSize)
	}

	if tree.MemtableCreatedAt != 5.0 {
		t.Errorf("Expected memtable created at 5.0, got %.1f", tree.MemtableCreatedAt)
	}

	// Add more writes
	tree.AddWrite(20.0, 10.0)

	if tree.MemtableCurrentSize != 30.0 {
		t.Errorf("Expected memtable size 30.0, got %.1f", tree.MemtableCurrentSize)
	}

	// MemtableCreatedAt should remain unchanged (set on first write)
	if tree.MemtableCreatedAt != 5.0 {
		t.Errorf("Expected memtable created at 5.0, got %.1f", tree.MemtableCreatedAt)
	}
}

func TestLSMTreeFlushMemtable(t *testing.T) {
	tree := NewLSMTree(3, 64.0)

	// Add some data to memtable
	tree.AddWrite(50.0, 0.0)

	// Flush should return an SST file and reset memtable
	file := tree.FlushMemtable(10.0)

	if file == nil {
		t.Fatal("Expected SST file, got nil")
	}

	if file.SizeMB != 50.0 {
		t.Errorf("Expected flushed size 50.0, got %.1f", file.SizeMB)
	}

	if tree.MemtableCurrentSize != 0.0 {
		t.Errorf("Expected memtable reset to 0.0, got %.1f", tree.MemtableCurrentSize)
	}

	if tree.MemtableCreatedAt != 10.0 {
		t.Errorf("Expected memtable created at 10.0, got %.1f", tree.MemtableCreatedAt)
	}

	// Total size should remain unchanged (data still in tree)
	if tree.TotalSizeMB != 50.0 {
		t.Errorf("Expected total size 50.0, got %.1f", tree.TotalSizeMB)
	}
}

func TestLSMTreeCreateSSTFile(t *testing.T) {
	tree := NewLSMTree(3, 64.0)

	file := tree.CreateSSTFile(0, 100.0, 20.0)

	if file == nil {
		t.Fatal("Expected file, got nil")
	}

	if file.SizeMB != 100.0 {
		t.Errorf("Expected size 100.0, got %.1f", file.SizeMB)
	}

	if file.CreatedAt != 20.0 {
		t.Errorf("Expected created at 20.0, got %.1f", file.CreatedAt)
	}

	// Verify file was added to L0
	if tree.Levels[0].FileCount != 1 {
		t.Errorf("Expected 1 file in L0, got %d", tree.Levels[0].FileCount)
	}

	if tree.Levels[0].TotalSize != 100.0 {
		t.Errorf("Expected L0 size 100.0, got %.1f", tree.Levels[0].TotalSize)
	}
}

func TestCalculateCompactionScoreL0(t *testing.T) {
	config := SimConfig{
		NumLevels:              3,
		L0CompactionTrigger:    4,
		MaxBytesForLevelBaseMB: 256,
		LevelMultiplier:        10,
	}

	tree := NewLSMTree(3, 64.0)

	t.Run("empty L0", func(t *testing.T) {
		score := tree.calculateCompactionScore(0, config, 0.0)
		if score != 0.0 {
			t.Errorf("Expected score 0.0 for empty L0, got %.2f", score)
		}
	})

	t.Run("L0 with 2 files (below trigger)", func(t *testing.T) {
		tree.Levels[0].AddSize(64, 1.0)
		tree.Levels[0].AddSize(64, 2.0)

		score := tree.calculateCompactionScore(0, config, 0.0)
		expected := 2.0 / 4.0 // 2 files / 4 trigger
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})

	t.Run("L0 at trigger threshold", func(t *testing.T) {
		tree = NewLSMTree(3, 64.0)
		for i := 0; i < 4; i++ {
			tree.Levels[0].AddSize(64, float64(i))
		}

		score := tree.calculateCompactionScore(0, config, 0.0)
		expected := 4.0 / 4.0 // 4 files / 4 trigger = 1.0
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})

	t.Run("L0 over trigger", func(t *testing.T) {
		tree = NewLSMTree(3, 64.0)
		for i := 0; i < 8; i++ {
			tree.Levels[0].AddSize(64, float64(i))
		}

		score := tree.calculateCompactionScore(0, config, 0.0)
		expected := 8.0 / 4.0 // 8 files / 4 trigger = 2.0
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})
}

func TestCalculateCompactionScoreL1Plus(t *testing.T) {
	config := SimConfig{
		NumLevels:                        3,
		L0CompactionTrigger:              4,
		MaxBytesForLevelBaseMB:           256,
		LevelMultiplier:                  10,
		LevelCompactionDynamicLevelBytes: false,
	}

	tree := NewLSMTree(3, 64.0)

	t.Run("L1 empty", func(t *testing.T) {
		score := tree.calculateCompactionScore(1, config, 0.0)
		if score != 0.0 {
			t.Errorf("Expected score 0.0 for empty L1, got %.2f", score)
		}
	})

	t.Run("L1 at target", func(t *testing.T) {
		// Target for L1 = MaxBytesForLevelBaseMB = 256
		tree.Levels[1].AddSize(256, 1.0)

		score := tree.calculateCompactionScore(1, config, 0.0)
		expected := 256.0 / 256.0 // = 1.0
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})

	t.Run("L1 over target", func(t *testing.T) {
		tree = NewLSMTree(3, 64.0)
		tree.Levels[1].AddSize(512, 1.0)

		score := tree.calculateCompactionScore(1, config, 0.0)
		expected := 512.0 / 256.0 // = 2.0
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})

	t.Run("L2 at target", func(t *testing.T) {
		tree = NewLSMTree(3, 64.0)
		// Target for L2 = 256 * 10^(2-1) = 2560
		tree.Levels[2].AddSize(2560, 1.0)

		score := tree.calculateCompactionScore(2, config, 0.0)
		expected := 2560.0 / 2560.0 // = 1.0
		if score != expected {
			t.Errorf("Expected score %.2f, got %.2f", expected, score)
		}
	})
}

func TestCalculateLevelTargetsSimpleMode(t *testing.T) {
	config := SimConfig{
		NumLevels:                        7,
		MaxBytesForLevelBaseMB:           256,
		LevelMultiplier:                  10,
		LevelCompactionDynamicLevelBytes: false,
	}

	tree := NewLSMTree(7, 64.0)
	targets := tree.calculateLevelTargets(config)

	if len(targets) != 7 {
		t.Fatalf("Expected 7 targets, got %d", len(targets))
	}

	// L0 target is MaxBytesForLevelBaseMB
	if targets[0] != 256.0 {
		t.Errorf("L0: expected target 256.0, got %.1f", targets[0])
	}

	// L1 target = 256 * 10^(1-1) = 256
	if targets[1] != 256.0 {
		t.Errorf("L1: expected target 256.0, got %.1f", targets[1])
	}

	// L2 target = 256 * 10^(2-1) = 2560
	if targets[2] != 2560.0 {
		t.Errorf("L2: expected target 2560.0, got %.1f", targets[2])
	}

	// L3 target = 256 * 10^(3-1) = 25600
	if targets[3] != 25600.0 {
		t.Errorf("L3: expected target 25600.0, got %.1f", targets[3])
	}
}

func TestCalculateLevelTargetsDynamicMode(t *testing.T) {
	config := SimConfig{
		NumLevels:                        7,
		MaxBytesForLevelBaseMB:           256,
		LevelMultiplier:                  10,
		LevelCompactionDynamicLevelBytes: true,
	}

	tree := NewLSMTree(7, 64.0)
	targets := tree.calculateLevelTargets(config)

	if len(targets) != 7 {
		t.Fatalf("Expected 7 targets, got %d", len(targets))
	}

	// L0 always uses MaxBytesForLevelBaseMB
	if targets[0] != 256.0 {
		t.Errorf("L0: expected target 256.0, got %.1f", targets[0])
	}

	// Last level (L6): 256 * 10^(6-1) = 256 * 100000 = 25,600,000
	lastLevel := 6
	expectedLast := 256.0 * 100000.0 // 10^5 = 100,000
	if targets[lastLevel] != expectedLast {
		t.Errorf("L6: expected target %.1f, got %.1f", expectedLast, targets[lastLevel])
	}

	// Targets should decrease as we go back (divide by multiplier)
	for level := lastLevel - 1; level >= 1; level-- {
		expected := targets[level+1] / 10.0
		minTarget := 256.0 / 10.0

		if expected < minTarget {
			// Should be marked as skipped (0)
			if targets[level] != 0.0 {
				t.Errorf("L%d: expected 0 (skipped), got %.1f", level, targets[level])
			}
		} else {
			if targets[level] != expected {
				t.Errorf("L%d: expected %.1f, got %.1f", level, expected, targets[level])
			}
		}
	}
}

func TestCalculateCompactionScoreInvalidLevel(t *testing.T) {
	config := SimConfig{
		NumLevels:              3,
		MaxBytesForLevelBaseMB: 256,
		LevelMultiplier:        10,
	}

	tree := NewLSMTree(3, 64.0)

	// Negative level
	score := tree.calculateCompactionScore(-1, config, 0.0)
	if score != 0.0 {
		t.Errorf("Expected score 0.0 for negative level, got %.2f", score)
	}

	// Out of range level
	score = tree.calculateCompactionScore(99, config, 0.0)
	if score != 0.0 {
		t.Errorf("Expected score 0.0 for out of range level, got %.2f", score)
	}
}

func TestLSMTreeState(t *testing.T) {
	tree := NewLSMTree(3, 64.0)

	// Add some data
	tree.AddWrite(50.0, 0.0)
	tree.Levels[0].AddSize(100, 5.0)
	tree.Levels[1].AddSize(250, 3.0)

	// Update TotalSizeMB manually (normally done by AddFile/AddSize)
	tree.TotalSizeMB = 100 + 250 // L0 + L1 (memtable doesn't count toward TotalSizeMB)

	virtualTime := 20.0
	config := DefaultConfig()
	state := tree.State(virtualTime, config)

	// Check memtable
	if state["memtableCurrentSizeMB"] != 50.0 {
		t.Errorf("Expected memtable size 50.0, got %v", state["memtableCurrentSizeMB"])
	}

	// Check total size (excludes memtable)
	if state["totalSizeMB"] != 350.0 {
		t.Errorf("Expected total size 350.0, got %v", state["totalSizeMB"])
	}

	// Check levels
	levels := state["levels"].([]map[string]interface{})
	if len(levels) != 3 {
		t.Fatalf("Expected 3 levels, got %d", len(levels))
	}

	// Check L0
	if levels[0]["fileCount"] != 1 {
		t.Errorf("L0: expected 1 file, got %v", levels[0]["fileCount"])
	}

	if levels[0]["totalSizeMB"] != 100.0 {
		t.Errorf("L0: expected size 100.0, got %v", levels[0]["totalSizeMB"])
	}

	// Check file age calculation
	files := levels[0]["files"].([]map[string]interface{})
	if len(files) > 0 {
		age := files[0]["ageSeconds"].(float64)
		expectedAge := virtualTime - 5.0 // Created at 5.0
		if age != expectedAge {
			t.Errorf("Expected age %.1f, got %.1f", expectedAge, age)
		}
	}
}

// TestCompactingSizeExclusion tests that files being compacted are excluded
// from score calculation (RocksDB's level_bytes_no_compacting behavior)
func TestCompactingSizeExclusion(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 3
	config.MaxBytesForLevelBaseMB = 256

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Add 300MB to L1 (exceeds 256MB target)
	tree.Levels[1].AddSize(300.0, 1.0)

	// Without compacting bytes: score = 300 / 256 = 1.17
	score := tree.calculateCompactionScore(1, config, 0.0)
	expectedScore := 300.0 / 256.0
	if score != expectedScore {
		t.Errorf("Expected score %.2f, got %.2f", expectedScore, score)
	}

	// Mark 100MB as being compacted
	tree.Levels[1].CompactingSize = 100.0

	// With compacting bytes: score = (300 - 100) / 256 = 200 / 256 = 0.78
	score = tree.calculateCompactionScore(1, config, 0.0)
	expectedScore = 200.0 / 256.0
	if score != expectedScore {
		t.Errorf("With compacting bytes: expected score %.2f, got %.2f", expectedScore, score)
	}

	// Verify score dropped below 1.0 (no longer needs compaction)
	if score >= 1.0 {
		t.Errorf("Score should be < 1.0 when compacting bytes reduce level below target, got %.2f", score)
	}
}

// TestTotalDowncompactBytes tests the helper function for static mode
func TestTotalDowncompactBytes(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 5
	config.MaxBytesForLevelBaseMB = 256
	config.LevelCompactionDynamicLevelBytes = false // Static mode (default)

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Setup: L1 = 600MB (exceeds 256MB target)
	tree.Levels[1].AddSize(600.0, 1.0)

	// In static mode: simple ratio, no kScoreScale
	// Score = 600 / 256 = 2.34
	score := tree.calculateCompactionScore(1, config, 0.0)
	expectedScore := 600.0 / 256.0
	tolerance := 0.01
	if score < expectedScore-tolerance || score > expectedScore+tolerance {
		t.Errorf("Static mode score: expected %.2f, got %.2f", expectedScore, score)
	}

	// In static mode, totalDowncompactBytes is calculated but not used in scoring
	// Score remains the same
	totalDowncompactBytes := 300.0
	scoreWithDowncompact := tree.calculateCompactionScore(1, config, totalDowncompactBytes)
	if scoreWithDowncompact != score {
		t.Errorf("Static mode: score should be unchanged with downcompact, %.2f vs %.2f",
			score, scoreWithDowncompact)
	}

	// TODO(fidelity): Add full dynamic mode test when full CalculateBaseBytes is implemented
}

// TestDynamicModeKScoreScale tests that kScoreScale is only applied when totalDowncompactBytes > 0
func TestDynamicModeKScoreScale(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 3
	config.MaxBytesForLevelBaseMB = 256
	config.LevelCompactionDynamicLevelBytes = true // Enable dynamic mode

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Setup: L1 = 300MB (exceeds 256MB target)
	tree.Levels[1].AddSize(300.0, 1.0)

	// Case 1: No incoming data from upper levels
	// Should use simple ratio (no kScoreScale)
	scoreNoDowncompact := tree.calculateCompactionScore(1, config, 0.0)
	expectedSimple := 300.0 / 256.0 // 1.17
	tolerance := 0.01
	if scoreNoDowncompact < expectedSimple-tolerance || scoreNoDowncompact > expectedSimple+tolerance {
		t.Errorf("Dynamic mode with no downcompact: expected %.2f, got %.2f", expectedSimple, scoreNoDowncompact)
	}

	// Case 2: With incoming data from upper levels
	// Should use kScoreScale formula: size / (target + downcompact) * 10.0
	totalDowncompactBytes := 100.0
	scoreWithDowncompact := tree.calculateCompactionScore(1, config, totalDowncompactBytes)
	expectedWithScale := (300.0 / (256.0 + 100.0)) * 10.0 // ~8.43
	if scoreWithDowncompact < expectedWithScale-tolerance || scoreWithDowncompact > expectedWithScale+tolerance {
		t.Errorf("Dynamic mode with downcompact: expected %.2f, got %.2f", expectedWithScale, scoreWithDowncompact)
	}

	// Verify that kScoreScale makes the score higher (keeps it > 1.0)
	// Without kScoreScale, score would be 300/(256+100) = 0.84, which is < 1.0
	// With kScoreScale, score is ~8.43, which is > 1.0
	if scoreWithDowncompact < 1.0 {
		t.Errorf("Dynamic mode kScoreScale should keep score > 1.0, got %.2f", scoreWithDowncompact)
	}

	// Case 3: Level under target should NOT apply kScoreScale even with downcompact
	tree2 := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
	tree2.Levels[1].AddSize(200.0, 1.0) // Under 256MB target
	scoreUnderTarget := tree2.calculateCompactionScore(1, config, 100.0)
	expectedUnderTarget := 200.0 / 256.0 // 0.78 (simple ratio)
	if scoreUnderTarget < expectedUnderTarget-tolerance || scoreUnderTarget > expectedUnderTarget+tolerance {
		t.Errorf("Dynamic mode under target: expected %.2f, got %.2f", expectedUnderTarget, scoreUnderTarget)
	}
}

// TestStaticModeScoring tests that static mode uses simple ratio without kScoreScale
func TestStaticModeScoring(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 3
	config.MaxBytesForLevelBaseMB = 256
	config.LevelCompactionDynamicLevelBytes = false // Static mode (default)

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	tests := []struct {
		name          string
		levelSize     float64
		expectedScore float64
	}{
		{
			name:          "Under target",
			levelSize:     200.0,
			expectedScore: 200.0 / 256.0, // 0.78
		},
		{
			name:          "At target",
			levelSize:     256.0,
			expectedScore: 1.0,
		},
		{
			name:          "Over target",
			levelSize:     300.0,
			expectedScore: 300.0 / 256.0, // 1.17
		},
		{
			name:          "2x over target",
			levelSize:     512.0,
			expectedScore: 512.0 / 256.0, // 2.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree.Levels[1].TotalSize = tt.levelSize
			tree.Levels[1].CompactingSize = 0

			score := tree.calculateCompactionScore(1, config, 0.0)

			tolerance := 0.01
			if score < tt.expectedScore-tolerance || score > tt.expectedScore+tolerance {
				t.Errorf("Expected score %.2f, got %.2f", tt.expectedScore, score)
			}

			// Static mode: score should always be simple ratio (never >= 10.0 for these inputs)
			if score >= 10.0 {
				t.Errorf("Static mode should not apply kScoreScale, got %.2f", score)
			}
		})
	}

	// TODO(fidelity): Add dynamic mode scoring tests when full CalculateBaseBytes is implemented
}

// TestCalculateTotalDowncompactBytes tests the helper function
func TestCalculateTotalDowncompactBytes(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 4
	config.MaxBytesForLevelBaseMB = 256
	config.LevelMultiplier = 10

	tree := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Targets: L1=256, L2=2560, L3=25600
	// Add data exceeding targets:
	tree.Levels[1].AddSize(300.0, 1.0)   // Exceeds by 44MB
	tree.Levels[2].AddSize(3000.0, 1.0)  // Exceeds by 440MB
	tree.Levels[3].AddSize(20000.0, 1.0) // Under target

	totalDowncompact := calculateTotalDowncompactBytes(tree, config)

	// Expected: 44 + 440 = 484MB (L3 is under target, doesn't contribute)
	expectedTotal := (300.0 - 256.0) + (3000.0 - 2560.0)
	tolerance := 0.01
	if totalDowncompact < expectedTotal-tolerance || totalDowncompact > expectedTotal+tolerance {
		t.Errorf("Expected total_downcompact_bytes %.2f, got %.2f", expectedTotal, totalDowncompact)
	}
}
