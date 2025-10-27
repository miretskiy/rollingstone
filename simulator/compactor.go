package simulator

import (
	"fmt"
	"math"
	"math/rand"
)

// filePicker interface for selecting files (internal to compactor)
type filePicker interface {
	Pick(min, max int) int
}

// Adapter to use distribution.go with existing code
type distributionAdapter struct {
	dist Distribution
	rng  *rand.Rand
}

func (da *distributionAdapter) Pick(min, max int) int {
	return da.dist.Sample(da.rng, min, max)
}

func newDistributionAdapter(distType DistributionType) filePicker {
	return &distributionAdapter{
		dist: NewDistribution(distType),
		rng:  rand.New(rand.NewSource(rand.Int63())), // Could be injected for testing
	}
}

// pickFileCount selects number of files to compact using distribution
func pickFileCount(availableFiles int, minFiles int, dist filePicker) int {
	if availableFiles <= minFiles {
		return availableFiles
	}
	return dist.Pick(minFiles, availableFiles)
}

// pickOverlapCount estimates overlapping files in target level
// Uses exponential distribution - high probability of 1-2 files, occasional massive overlaps
func pickOverlapCount(maxFiles int, dist filePicker) int {
	if maxFiles == 0 {
		return 0
	}
	return dist.Pick(1, maxFiles)
}

// Compactor interface for different compaction strategies
type Compactor interface {
	// NeedsCompaction checks if a level needs compaction
	NeedsCompaction(level int, lsm *LSMTree, config SimConfig) bool

	// PickCompaction selects files to compact and returns compaction job
	PickCompaction(level int, lsm *LSMTree, config SimConfig) *CompactionJob

	// ExecuteCompaction performs the compaction
	ExecuteCompaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64)
}

// CompactionJob describes a compaction operation
type CompactionJob struct {
	FromLevel   int
	ToLevel     int
	SourceFiles []*SSTFile // Files to compact from source level
	TargetFiles []*SSTFile // Overlapping files in target level
	IsIntraL0   bool       // True if this is intra-L0 compaction
}

// LeveledCompactor implements RocksDB-style leveled compaction
type LeveledCompactor struct {
	fileSelectDist    filePicker // For picking files from source level
	overlapSelectDist filePicker // For estimating overlaps in target level
}

// NewLeveledCompactor creates a compactor with default distributions
func NewLeveledCompactor() *LeveledCompactor {
	return &LeveledCompactor{
		fileSelectDist:    newDistributionAdapter(DistGeometric),   // Favor picking fewer files
		overlapSelectDist: newDistributionAdapter(DistExponential), // Most overlaps are small
	}
}

// NeedsCompaction checks if a level needs compaction based on scoring
func (c *LeveledCompactor) NeedsCompaction(level int, lsm *LSMTree, config SimConfig) bool {
	score := lsm.calculateCompactionScore(level, config)
	return score > 1.0
}

// PickCompaction selects files for compaction
func (c *LeveledCompactor) PickCompaction(level int, lsm *LSMTree, config SimConfig) *CompactionJob {
	if level < 0 || level >= len(lsm.Levels) {
		return nil
	}

	sourceLevel := lsm.Levels[level]

	// L0 compaction: always prefer L0 → L1 (the default RocksDB behavior)
	// Intra-L0 compaction is disabled for now - it's a rare optimization
	// that requires more sophisticated triggering logic
	if level == 0 {
		targetLevel := lsm.Levels[1]
		// Estimate overlap - L0 files typically overlap many L1 files
		numOverlaps := pickOverlapCount(targetLevel.FileCount, c.overlapSelectDist)
		targetFiles := selectFiles(targetLevel.Files, numOverlaps)

		return &CompactionJob{
			FromLevel:   0,
			ToLevel:     1,
			SourceFiles: sourceLevel.Files, // All L0 files
			TargetFiles: targetFiles,
			IsIntraL0:   false,
		}
	}

	// Ln → Ln+1: Pick 1-2 files from Ln, estimate overlaps in Ln+1
	if level+1 < len(lsm.Levels) {
		targetLevel := lsm.Levels[level+1]
		// Pick small number of files from source level
		numSourceFiles := pickFileCount(sourceLevel.FileCount, 1, c.fileSelectDist)
		sourceFiles := selectFiles(sourceLevel.Files, numSourceFiles)

		// Estimate overlaps in target level
		numOverlaps := pickOverlapCount(targetLevel.FileCount, c.overlapSelectDist)
		targetFiles := selectFiles(targetLevel.Files, numOverlaps)

		return &CompactionJob{
			FromLevel:   level,
			ToLevel:     level + 1,
			SourceFiles: sourceFiles,
			TargetFiles: targetFiles,
			IsIntraL0:   false,
		}
	}

	return nil
}

// ExecuteCompaction performs the compaction and returns input/output sizes
func (c *LeveledCompactor) ExecuteCompaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64) {
	if job == nil {
		return 0, 0
	}

	// Calculate input size
	for _, f := range job.SourceFiles {
		inputSize += f.SizeMB
	}
	for _, f := range job.TargetFiles {
		inputSize += f.SizeMB
	}

	// Calculate output size based on reduction factor
	var reductionFactor float64
	if job.FromLevel == 0 && job.ToLevel == 1 {
		// L0→L1: significant deduplication (10% reduction)
		reductionFactor = 0.9
	} else {
		// Deeper levels: minimal deduplication (1% reduction)
		reductionFactor = 0.99
	}

	outputSize = inputSize * reductionFactor

	// Handle intra-L0 compaction
	if job.IsIntraL0 {
		// Remove source files, add output as new L0 files
		lsm.Levels[0].removeFiles(job.SourceFiles)
		numOutputFiles := max(1, len(job.SourceFiles)/2) // Merge into fewer files (int)
		avgFileSize := outputSize / float64(numOutputFiles)
		for i := 0; i < numOutputFiles; i++ {
			lsm.Levels[0].AddSize(avgFileSize, virtualTime)
		}
		// DEBUG
		fmt.Printf("[COMPACTION] Intra-L0: removed %d files, added %d files, L0 now has %d files\n",
			len(job.SourceFiles), numOutputFiles, lsm.Levels[0].FileCount)
		return inputSize, outputSize
	}

	// DEBUG: Before compaction
	fmt.Printf("[COMPACTION] L%d→L%d: Before - L%d has %d files (%.1f MB), L%d has %d files (%.1f MB)\n",
		job.FromLevel, job.ToLevel,
		job.FromLevel, lsm.Levels[job.FromLevel].FileCount, lsm.Levels[job.FromLevel].TotalSize,
		job.ToLevel, lsm.Levels[job.ToLevel].FileCount, lsm.Levels[job.ToLevel].TotalSize)
	fmt.Printf("[COMPACTION] Removing %d source files, %d target files, adding %.1f MB output\n",
		len(job.SourceFiles), len(job.TargetFiles), outputSize)

	// Remove files from source and target levels
	lsm.Levels[job.FromLevel].removeFiles(job.SourceFiles)
	lsm.Levels[job.ToLevel].removeFiles(job.TargetFiles)

	// Split output into multiple files based on target_file_size
	// In RocksDB: target_file_size = target_file_size_base * (target_file_size_multiplier ^ (level-1))
	// For L1: base * multiplier^0 = 64MB
	// For L2: base * multiplier^1 = 128MB (if multiplier=2)
	// For L3: base * multiplier^2 = 256MB, etc.
	// This creates progressively larger files in deeper levels (up to ~2GB at L6)
	targetFileSizeMB := float64(config.TargetFileSizeMB)
	if job.ToLevel > 0 {
		// Apply multiplier: level 1 uses base, level 2 uses base*mult^1, etc.
		multiplier := float64(config.TargetFileSizeMultiplier)
		for i := 1; i < job.ToLevel; i++ {
			targetFileSizeMB *= multiplier
		}
		// Cap at 2GB per file (reasonable maximum)
		if targetFileSizeMB > 2048.0 {
			targetFileSizeMB = 2048.0
		}
	}

	numOutputFiles := int(math.Ceil(outputSize / targetFileSizeMB))
	if numOutputFiles < 1 {
		numOutputFiles = 1
	}

	avgFileSize := outputSize / float64(numOutputFiles)
	for i := 0; i < numOutputFiles; i++ {
		lsm.Levels[job.ToLevel].AddSize(avgFileSize, virtualTime)
	}

	// DEBUG: After compaction
	fmt.Printf("[COMPACTION] L%d→L%d: After - L%d has %d files (%.1f MB), L%d has %d files (%.1f MB), created %d output files\n",
		job.FromLevel, job.ToLevel,
		job.FromLevel, lsm.Levels[job.FromLevel].FileCount, lsm.Levels[job.FromLevel].TotalSize,
		job.ToLevel, lsm.Levels[job.ToLevel].FileCount, lsm.Levels[job.ToLevel].TotalSize,
		numOutputFiles)

	return inputSize, outputSize
}

// selectFiles picks first N files from the level (simulates oldest-first or round-robin)
func selectFiles(files []*SSTFile, count int) []*SSTFile {
	if count >= len(files) {
		return files
	}
	if count <= 0 {
		return nil
	}
	return files[:count]
}

// removeFiles removes specified files from a level
func (l *Level) removeFiles(filesToRemove []*SSTFile) {
	if len(filesToRemove) == 0 {
		return
	}

	// Create map for O(1) lookup
	toRemove := make(map[*SSTFile]bool)
	for _, f := range filesToRemove {
		toRemove[f] = true
	}

	// Filter out files to remove
	newFiles := make([]*SSTFile, 0, len(l.Files))
	var removedSize float64
	for _, f := range l.Files {
		if !toRemove[f] {
			newFiles = append(newFiles, f)
		} else {
			removedSize += f.SizeMB
		}
	}

	l.Files = newFiles
	l.FileCount = len(newFiles)
	l.TotalSize -= removedSize
}
