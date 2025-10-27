package simulator

import (
	"fmt"
	"math"
)

// SSTFile represents a single SST file
type SSTFile struct {
	ID        string  `json:"id"`
	SizeMB    float64 `json:"sizeMB"`
	CreatedAt float64 `json:"createdAt"` // Virtual time when created
}

// AgeSeconds returns the age of the file at given virtual time
func (f *SSTFile) AgeSeconds(virtualTime float64) float64 {
	return virtualTime - f.CreatedAt
}

// Level represents one level in the LSM tree
type Level struct {
	Number    int        `json:"level"`
	Files     []*SSTFile `json:"files"`
	TotalSize float64    `json:"totalSizeMB"`
	FileCount int        `json:"fileCount"`
}

// NewLevel creates a new level
func NewLevel(number int) *Level {
	return &Level{
		Number:    number,
		Files:     make([]*SSTFile, 0),
		TotalSize: 0,
		FileCount: 0,
	}
}

// AddFile adds a file to the level
func (l *Level) AddFile(file *SSTFile) {
	l.Files = append(l.Files, file)
	l.TotalSize += file.SizeMB
	l.FileCount++
}

// AddSize adds data of given size to the level (creates a virtual file)
// Used by compaction when we don't need to track individual file details
func (l *Level) AddSize(sizeMB float64, virtualTime float64) {
	// Create a single virtual file representing the compacted data
	file := &SSTFile{
		ID:        fmt.Sprintf("sst-%d-%d", l.Number, len(l.Files)),
		SizeMB:    sizeMB,
		CreatedAt: virtualTime,
	}
	l.AddFile(file)
}

// RemoveFiles removes files from the level
func (l *Level) RemoveFiles(filesToRemove []*SSTFile) {
	// Create a map of file IDs to remove
	removeMap := make(map[string]bool)
	for _, f := range filesToRemove {
		removeMap[f.ID] = true
	}

	// Filter out files to remove
	newFiles := make([]*SSTFile, 0, len(l.Files)-len(filesToRemove))
	newTotalSize := 0.0
	for _, f := range l.Files {
		if !removeMap[f.ID] {
			newFiles = append(newFiles, f)
			newTotalSize += f.SizeMB
		}
	}

	l.Files = newFiles
	l.TotalSize = newTotalSize
	l.FileCount = len(newFiles)
}

// LSMTree represents the entire LSM tree structure
type LSMTree struct {
	Levels              []*Level `json:"levels"`
	MemtableCurrentSize float64  `json:"memtableCurrentSizeMB"`
	MemtableMaxSize     float64  `json:"memtableMaxSizeMB"`
	MemtableCreatedAt   float64  `json:"memtableCreatedAt"` // Virtual time when memtable was created
	TotalSizeMB         float64  `json:"totalSizeMB"`

	// Counters for generating unique IDs
	nextFileID int64
}

// NewLSMTree creates a new LSM tree
func NewLSMTree(numLevels int, memtableMaxSize float64) *LSMTree {
	levels := make([]*Level, numLevels)
	for i := 0; i < numLevels; i++ {
		levels[i] = NewLevel(i)
	}

	return &LSMTree{
		Levels:              levels,
		MemtableCurrentSize: 0,
		MemtableMaxSize:     memtableMaxSize,
		MemtableCreatedAt:   0,
		TotalSizeMB:         0,
		nextFileID:          1,
	}
}

// AddWrite adds data to the memtable
func (t *LSMTree) AddWrite(sizeMB float64, virtualTime float64) {
	// If this is the first write to an empty memtable, record the creation time
	if t.MemtableCurrentSize == 0 {
		t.MemtableCreatedAt = virtualTime
	}
	t.MemtableCurrentSize += sizeMB
}

// NeedsFlush returns true if memtable should be flushed
// Checks both size-based and time-based triggers
func (t *LSMTree) NeedsFlush(virtualTime float64, timeoutSec float64) bool {
	// Size-based trigger (always check this)
	if t.MemtableCurrentSize >= t.MemtableMaxSize {
		return true
	}

	// Time-based trigger (only if configured and memtable has data)
	if timeoutSec > 0 && t.MemtableCurrentSize > 0 {
		age := virtualTime - t.MemtableCreatedAt
		if age >= timeoutSec {
			return true
		}
	}

	return false
}

// FlushMemtable flushes memtable to L0 and returns the new SST file
func (t *LSMTree) FlushMemtable(virtualTime float64) *SSTFile {
	if t.MemtableCurrentSize == 0 {
		return nil
	}

	file := &SSTFile{
		ID:        fmt.Sprintf("sst-%d", t.nextFileID),
		SizeMB:    t.MemtableCurrentSize,
		CreatedAt: virtualTime,
	}
	t.nextFileID++

	// Add to L0
	t.Levels[0].AddFile(file)
	t.TotalSizeMB += file.SizeMB

	// Clear memtable and reset creation time
	t.MemtableCurrentSize = 0
	t.MemtableCreatedAt = virtualTime

	return file
}

// CreateSSTFile creates an SST file at the specified level with given size
// This is used when flushing a frozen (immutable) memtable
func (t *LSMTree) CreateSSTFile(level int, sizeMB float64, virtualTime float64) *SSTFile {
	if level < 0 || level >= len(t.Levels) || sizeMB <= 0 {
		return nil
	}

	file := &SSTFile{
		ID:        fmt.Sprintf("sst-%d", t.nextFileID),
		SizeMB:    sizeMB,
		CreatedAt: virtualTime,
	}
	t.nextFileID++

	// Add to specified level
	t.Levels[level].AddFile(file)
	t.TotalSizeMB += file.SizeMB

	return file
}

// NeedsCompaction checks if a level needs compaction
func (t *LSMTree) NeedsCompaction(level int, l0Trigger int, maxBytesForLevelBase float64, multiplier int) bool {
	if level < 0 || level >= len(t.Levels) {
		return false
	}

	if level == 0 {
		// L0 uses file count trigger
		return t.Levels[0].FileCount >= l0Trigger
	}

	// L1+ use size triggers
	targetSize := maxBytesForLevelBase * math.Pow(float64(multiplier), float64(level-1))
	return t.Levels[level].TotalSize > targetSize
}

// CompactLevel performs compaction from one level to the next
// Returns input size and output size in MB
func (t *LSMTree) CompactLevel(fromLevel, toLevel int, reductionFactor float64, virtualTime float64) (inputSizeMB, outputSizeMB float64) {
	if fromLevel < 0 || fromLevel >= len(t.Levels)-1 {
		return 0, 0
	}
	if toLevel != fromLevel+1 {
		return 0, 0
	}

	sourceLevel := t.Levels[fromLevel]
	targetLevel := t.Levels[toLevel]

	if sourceLevel.FileCount == 0 {
		return 0, 0
	}

	// For L0->L1: compact all files
	// For Ln->Ln+1: compact all files (simplified for MVP)
	filesToCompact := sourceLevel.Files
	inputSizeMB = sourceLevel.TotalSize

	// Apply reduction factor (deduplication + compression)
	outputSizeMB = inputSizeMB * reductionFactor

	// Create new file in target level
	newFile := &SSTFile{
		ID:        fmt.Sprintf("sst-%d", t.nextFileID),
		SizeMB:    outputSizeMB,
		CreatedAt: virtualTime,
	}
	t.nextFileID++

	// Remove files from source level
	sourceLevel.RemoveFiles(filesToCompact)

	// Add new file to target level
	targetLevel.AddFile(newFile)

	// Update total size
	t.TotalSizeMB = t.TotalSizeMB - inputSizeMB + outputSizeMB

	return inputSizeMB, outputSizeMB
}

// Removed: use math.Pow instead

// calculateCompactionScore calculates the compaction score for a level
// Higher score means more urgent need for compaction
func (t *LSMTree) calculateCompactionScore(level int, config SimConfig) float64 {
	if level < 0 || level >= len(t.Levels) {
		return 0.0
	}

	levelState := t.Levels[level]

	if level == 0 {
		// L0 score = max(fileCount / trigger, totalSize / max_bytes_for_level_base)
		fileScore := float64(levelState.FileCount) / float64(config.L0CompactionTrigger)
		sizeScore := levelState.TotalSize / float64(config.MaxBytesForLevelBaseMB)
		if fileScore > sizeScore {
			return fileScore
		}
		return sizeScore
	}

	// For Ln (n >= 1): score = totalSize / targetSize
	targets := t.calculateLevelTargets(config)
	if level >= len(targets) {
		return 0.0
	}

	targetSize := targets[level]
	if targetSize <= 0 {
		return 0.0
	}

	return levelState.TotalSize / targetSize
}

// calculateLevelTargets computes target sizes for each level
func (t *LSMTree) calculateLevelTargets(config SimConfig) []float64 {
	targets := make([]float64, len(t.Levels))

	if config.LevelCompactionDynamicLevelBytes {
		// Dynamic mode: work backwards from largest level
		// This ensures 90% of data stays in the last level
		// Start from the last level and work backwards
		lastLevel := len(t.Levels) - 1
		targets[lastLevel] = float64(config.MaxBytesForLevelBaseMB) * math.Pow(float64(config.LevelMultiplier), float64(lastLevel-1))

		for level := lastLevel - 1; level >= 1; level-- {
			targets[level] = targets[level+1] / float64(config.LevelMultiplier)
			// Skip levels with target < max_bytes_for_level_base / multiplier
			minTarget := float64(config.MaxBytesForLevelBaseMB) / float64(config.LevelMultiplier)
			if targets[level] < minTarget {
				targets[level] = 0 // Mark as skipped
			}
		}
		targets[0] = float64(config.MaxBytesForLevelBaseMB) // L0 uses file count, not size
	} else {
		// Simple exponential sizing (more intuitive, default)
		targets[0] = float64(config.MaxBytesForLevelBaseMB) // L0 uses file count, not size
		for level := 1; level < len(t.Levels); level++ {
			targets[level] = float64(config.MaxBytesForLevelBaseMB) * math.Pow(float64(config.LevelMultiplier), float64(level-1))
		}
	}

	return targets
}

// pickLevelToCompact was removed - unused code
// Compaction scheduling logic is now in simulator.go:tryScheduleCompaction()

// State returns the current state for JSON serialization
func (t *LSMTree) State(virtualTime float64) map[string]interface{} {
	levels := make([]map[string]interface{}, len(t.Levels))
	for i, level := range t.Levels {
		// Limit to first 20 files per level to prevent browser memory exhaustion
		maxFiles := 20
		fileCount := len(level.Files)
		if fileCount > maxFiles {
			fileCount = maxFiles
		}

		files := make([]map[string]interface{}, fileCount)
		for j := 0; j < fileCount; j++ {
			file := level.Files[j]
			files[j] = map[string]interface{}{
				"id":         file.ID,
				"sizeMB":     file.SizeMB,
				"ageSeconds": virtualTime - file.CreatedAt,
			}
		}

		levels[i] = map[string]interface{}{
			"level":       level.Number,
			"totalSizeMB": level.TotalSize,
			"fileCount":   level.FileCount,
			"files":       files,
		}
	}

	return map[string]interface{}{
		"levels":                levels,
		"memtableCurrentSizeMB": t.MemtableCurrentSize,
		"totalSizeMB":           t.TotalSizeMB,
	}
}
