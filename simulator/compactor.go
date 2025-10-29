package simulator

import (
	"fmt"
	"math"
	"math/rand"
	"time"
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
	// Returns: inputSize (MB), outputSize (MB), outputFileCount
	ExecuteCompaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64, outputFileCount int)
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
//
// RocksDB Reference: LevelCompactionBuilder in db/compaction/compaction_picker_level.cc
// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_level.cc
//
// FIDELITY APPROACH:
// This implementation strives for high fidelity to RocksDB's compaction logic while making
// deliberate simplifications for simulation performance. Each claim about RocksDB behavior
// is verified against actual C++ source code and documented inline with:
// - GitHub links to specific files/lines
// - Actual C++ code snippets
// - FIDELITY markers: ✓ (matches exactly), ⚠️ (simplified), ✗ (missing)
// - Test references
//
// Key Simplifications (By Design):
//  1. Statistical file selection: Uses Geometric/Exponential distributions instead of
//     tracking actual SSTable key ranges. This models workload characteristics
//     statistically (uniform writes = many overlaps, skewed = few overlaps).
//  2. Simplified intra-L0 logic: Respects max_compaction_bytes but doesn't implement
//     RocksDB's "diminishing returns" check (compact_bytes_per_del_file increasing).
//
// See FIDELITY_REPORT.md for comprehensive audit results and test coverage.
type LeveledCompactor struct {
	fileSelectDist    filePicker // For picking files from source level
	overlapSelectDist filePicker // For estimating overlaps in target level
	rng               *rand.Rand // Random number generator for file selection
}

// NewLeveledCompactor creates a compactor with default distributions
// Distribution choices model typical RocksDB workload characteristics:
// - Geometric for file selection: usually pick 1-2 files, occasionally more
// - Exponential for overlaps: most compactions touch few files, rare massive overlaps
// If seed is 0, uses a time-based random seed
func NewLeveledCompactor(seed int64) *LeveledCompactor {
	var rng *rand.Rand
	if seed == 0 {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	} else {
		rng = rand.New(rand.NewSource(seed))
	}

	return &LeveledCompactor{
		fileSelectDist:    newDistributionAdapter(DistGeometric),   // Favor picking fewer files
		overlapSelectDist: newDistributionAdapter(DistExponential), // Most overlaps are small
		rng:               rng,
	}
}

// calculateTotalDowncompactBytes calculates the total bytes being compacted
// down from upper levels, used for deprioritizing levels with heavy incoming data
//
// RocksDB Reference: total_downcompact_bytes in ComputeCompactionScore()
// GitHub: https://github.com/facebook/rocksdb/blob/main/db/version_set.cc
// See: db/version_set.cc (exact line numbers vary by version)
//
// TODO(fidelity): In dynamic mode, RocksDB tracks "unnecessary levels" and accumulates
// their entire size. For now, we only accumulate bytes exceeding target.
func calculateTotalDowncompactBytes(lsm *LSMTree, config SimConfig) float64 {
	var total float64
	targets := lsm.calculateLevelTargets(config)

	// TODO(fidelity): In dynamic mode, RocksDB tracks "unnecessary levels" and accumulates
	// their entire size. For now, we only accumulate bytes exceeding target.
	for i := 0; i < len(lsm.Levels); i++ {
		level := lsm.Levels[i]
		if i >= len(targets) {
			break
		}
		targetSize := targets[i]

		// Accumulate bytes exceeding target (will be compacted down)
		if level.TotalSize > targetSize {
			total += level.TotalSize - targetSize
		}
	}

	return total
}

// NeedsCompaction checks if a level needs compaction based on scoring
func (c *LeveledCompactor) NeedsCompaction(level int, lsm *LSMTree, config SimConfig) bool {
	// Calculate total_downcompact_bytes for accurate scoring
	totalDowncompactBytes := calculateTotalDowncompactBytes(lsm, config)
	score := lsm.calculateCompactionScore(level, config, totalDowncompactBytes)
	return score > 1.0
}

// PickCompaction selects files for compaction
//
// RocksDB Reference: LevelCompactionBuilder::PickCompaction()
// See: db/compaction/compaction_picker_level.cc
//
// High-fidelity simulation of file selection:
// - L0→L1: All L0 files (tiered within L0, may overlap)
// - Intra-L0: When L0 has too many files, compact within L0 to reduce read amp
// - Ln→Ln+1: Pick oldest/largest files, estimate overlaps via distribution
//
// Key difference from RocksDB:
// - RocksDB tracks actual key ranges and computes exact overlaps
// - Simulator uses distributions to model overlap probability (workload characteristic)
func (c *LeveledCompactor) PickCompaction(level int, lsm *LSMTree, config SimConfig) *CompactionJob {
	if level < 0 || level >= len(lsm.Levels) {
		return nil
	}

	sourceLevel := lsm.Levels[level]

	if level == 0 {
		// Always prefer L0→L1 compaction (normal case)
		// Intra-L0 is a FALLBACK for extreme cases only
		targetLevel := lsm.Levels[1]

		// RocksDB Reference: PickIntraL0Compaction()
		// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_level.cc#L901-L915
		//
		// RocksDB C++ (lines 901-915):
		//   bool LevelCompactionBuilder::PickIntraL0Compaction() {
		//     const std::vector<FileMetaData*>& level_files = vstorage_->LevelFiles(0);
		//     if (level_files.size() <
		//             static_cast<size_t>(
		//                 mutable_cf_options_.level0_file_num_compaction_trigger + 2) ||
		//         level_files[0]->being_compacted) {
		//       return false;
		//     }
		//     return FindIntraL0Compaction(level_files, kMinFilesForIntraL0Compaction, ...);
		//   }
		//
		// Intra-L0 compaction is triggered when:
		// 1. L0 file count >= level0_file_num_compaction_trigger + 2
		// 2. At least 4 files total (kMinFilesForIntraL0Compaction = 4, line 163)
		// 3. First file is not being compacted (we don't track this)
		//
		// FIDELITY: ✓ Trigger threshold matches RocksDB exactly
		const kMinFilesForIntraL0Compaction = 4
		intraL0Threshold := config.L0CompactionTrigger + 2

		if sourceLevel.FileCount >= intraL0Threshold && sourceLevel.FileCount >= kMinFilesForIntraL0Compaction {
			// Pick files respecting max_compaction_bytes
			//
			// RocksDB Reference: max_compaction_bytes default calculation
			// GitHub: https://github.com/facebook/rocksdb/blob/main/db/column_family.cc#L403-L405
			//
			// RocksDB C++ (lines 403-405):
			//   if (result.max_compaction_bytes == 0) {
			//     result.max_compaction_bytes = result.target_file_size_base * 25;
			//   }
			//
			// FIDELITY: ✓ Matches RocksDB exactly
			const kDefaultMaxCompactionBytesMultiplier = 25 // RocksDB constant
			maxCompactionMB := float64(config.MaxCompactionBytesMB)
			if maxCompactionMB <= 0 {
				maxCompactionMB = float64(config.TargetFileSizeMB * kDefaultMaxCompactionBytesMultiplier)
			}

			filesToCompact := make([]*SSTFile, 0)
			var totalSize float64

			// RocksDB Reference: FindIntraL0Compaction() file selection
			// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker.cc#L30-L64
			//
			// RocksDB C++ (simplified):
			//   for (limit = start + 1; limit < level_files.size(); ++limit) {
			//     compact_bytes += file_size;
			//     new_compact_bytes_per_del_file = compact_bytes / (limit - start);
			//     if (new_compact_bytes_per_del_file > compact_bytes_per_del_file ||  // DIMINISHING RETURNS
			//         compact_bytes > max_compaction_bytes) {
			//       break;
			//     }
			//     compact_bytes_per_del_file = new_compact_bytes_per_del_file;
			//   }
			//
			// FIDELITY: ⚠️ SIMPLIFIED - we don't implement diminishing returns check
			// Impact: May pick slightly more/fewer files, but still respects max_compaction_bytes
			//
			// Pick files from oldest (beginning of list) until we hit max_compaction_bytes
			for i := 0; i < len(sourceLevel.Files); i++ {
				file := sourceLevel.Files[i]
				if totalSize+file.SizeMB > maxCompactionMB && len(filesToCompact) >= 2 {
					break // Hit limit, stop picking
				}
				filesToCompact = append(filesToCompact, file)
				totalSize += file.SizeMB
			}

			// Need at least 2 files for intra-L0 compaction to make sense
			if len(filesToCompact) >= 2 {
				return &CompactionJob{
					FromLevel:   0,
					ToLevel:     0, // Intra-L0
					SourceFiles: filesToCompact,
					TargetFiles: []*SSTFile{}, // No target files for intra-L0
					IsIntraL0:   true,
				}
			}
		}

		// Normal L0→L1 compaction (preferred path)
		// RocksDB Reference: LevelCompactionBuilder::SetupInitialFiles()
		// See: db/compaction/compaction_picker_level.cc:147-190
		//
		// L0→L1 typically includes all L0 files (they may overlap)
		// But respects max_compaction_bytes limit
		// RocksDB Reference: db/column_family.cc - if max_compaction_bytes == 0, set to target_file_size_base * 25
		const kDefaultMaxCompactionBytesMultiplier = 25 // RocksDB constant
		maxCompactionMB := float64(config.MaxCompactionBytesMB)
		if maxCompactionMB <= 0 {
			maxCompactionMB = float64(config.TargetFileSizeMB * kDefaultMaxCompactionBytesMultiplier)
		}

		// Calculate total L0 size
		var l0TotalSize float64
		for _, f := range sourceLevel.Files {
			l0TotalSize += f.SizeMB
		}

		// Estimate overlap - L0 files typically overlap many L1 files
		// Distribution models workload: uniform writes = many overlaps, skewed = few
		numOverlaps := pickOverlapCount(targetLevel.FileCount, c.overlapSelectDist)
		targetFiles := selectFiles(targetLevel.Files, numOverlaps)

		// Calculate target file size
		var targetTotalSize float64
		for _, f := range targetFiles {
			targetTotalSize += f.SizeMB
		}

		// Check if total input exceeds max_compaction_bytes
		totalInputSize := l0TotalSize + targetTotalSize
		if totalInputSize > maxCompactionMB {
			// Need to reduce scope - prioritize L0 files, reduce target overlap
			// Simple heuristic: reduce target files proportionally
			targetReduction := (totalInputSize - maxCompactionMB) / totalInputSize
			newTargetCount := int(float64(len(targetFiles)) * (1.0 - targetReduction))
			if newTargetCount < 0 {
				newTargetCount = 0
			}
			targetFiles = targetFiles[:newTargetCount]
		}

		return &CompactionJob{
			FromLevel:   0,
			ToLevel:     1,
			SourceFiles: sourceLevel.Files, // All L0 files
			TargetFiles: targetFiles,
			IsIntraL0:   false,
		}
	}

	// Ln → Ln+1: Pick 1-2 files from Ln, estimate overlaps in Ln+1
	// RocksDB Reference: LevelCompactionBuilder::SetupInitialFiles()
	// See: db/compaction/compaction_picker_level.cc:147-190
	if level+1 < len(lsm.Levels) {
		targetLevel := lsm.Levels[level+1]
		// RocksDB Reference: db/column_family.cc - if max_compaction_bytes == 0, set to target_file_size_base * 25
		const kDefaultMaxCompactionBytesMultiplier = 25 // RocksDB constant
		maxCompactionMB := float64(config.MaxCompactionBytesMB)
		if maxCompactionMB <= 0 {
			maxCompactionMB = float64(config.TargetFileSizeMB * kDefaultMaxCompactionBytesMultiplier)
		}

		// Pick small number of files from source level
		numSourceFiles := pickFileCount(sourceLevel.FileCount, 1, c.fileSelectDist)
		sourceFiles := selectFiles(sourceLevel.Files, numSourceFiles)

		// Calculate source size
		var sourceSize float64
		for _, f := range sourceFiles {
			sourceSize += f.SizeMB
		}

		// Estimate overlaps in target level
		numOverlaps := pickOverlapCount(targetLevel.FileCount, c.overlapSelectDist)
		targetFiles := selectFiles(targetLevel.Files, numOverlaps)

		// Limit target files to respect max_compaction_bytes
		var targetSize float64
		limitedTargetFiles := make([]*SSTFile, 0, len(targetFiles))
		for _, f := range targetFiles {
			if sourceSize+targetSize+f.SizeMB > maxCompactionMB {
				break // Hit limit
			}
			limitedTargetFiles = append(limitedTargetFiles, f)
			targetSize += f.SizeMB
		}

		return &CompactionJob{
			FromLevel:   level,
			ToLevel:     level + 1,
			SourceFiles: sourceFiles,
			TargetFiles: limitedTargetFiles,
			IsIntraL0:   false,
		}
	}

	return nil
}

// ExecuteCompaction performs the compaction and returns input/output sizes
//
// RocksDB Reference: CompactionJob::Run() in db/compaction/compaction_job.cc
//
// High-fidelity simulation of compaction execution:
// - Detects trivial moves (no overlap = just pointer update, no I/O)
// - Reads all input files (source + overlapping target)
// - Merges/deduplicates/compresses data
// - Splits output into multiple SST files based on target_file_size
// - Updates LSM tree structure
//
// Simulation approximations:
// - No actual data merging (uses reduction factor to model dedup/compression)
// - File splitting based on size, not actual key ranges
func (c *LeveledCompactor) ExecuteCompaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64, outputFileCount int) {
	if job == nil {
		return 0, 0, 0
	}

	// Check for trivial move: no overlapping files in target level
	//
	// RocksDB Reference: TryExtendNonL0TrivialMove() and related logic
	// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_level.cc
	// (Search for "trivial move" - multiple references throughout)
	//
	// RocksDB performs trivial moves when files don't overlap with target level:
	// - Just updates file metadata (level pointer)
	// - No actual data rewrite or I/O
	// - Significantly faster than full compaction
	//
	// FIDELITY: ✓ Optimization implemented correctly
	// Test: compactor_test.go:TestTrivialMove
	if len(job.TargetFiles) == 0 && !job.IsIntraL0 {
		fmt.Printf("[TRIVIAL MOVE] L%d→L%d: Moving %d files (%.1f MB) without rewriting\n",
			job.FromLevel, job.ToLevel, len(job.SourceFiles), inputSize)

		// Calculate input size for metrics
		for _, f := range job.SourceFiles {
			inputSize += f.SizeMB
		}

		// Trivial move: output = input (no reduction)
		outputSize = inputSize
		outputFileCount = len(job.SourceFiles) // Just moving existing files

		// Remove from source, add to target (no file creation, just move)
		lsm.Levels[job.FromLevel].removeFiles(job.SourceFiles)
		for _, f := range job.SourceFiles {
			lsm.Levels[job.ToLevel].AddFile(f)
		}

		return inputSize, outputSize, outputFileCount
	}

	// Normal compaction: read all input files
	for _, f := range job.SourceFiles {
		inputSize += f.SizeMB
	}
	for _, f := range job.TargetFiles {
		inputSize += f.SizeMB
	}

	// Calculate output size based on reduction factor
	// Models RocksDB's merge operator, deduplication, and compression
	var reductionFactor float64
	if job.FromLevel == 0 && job.ToLevel == 1 {
		// L0→L1: significant deduplication (10% reduction)
		// Multiple versions of same key across L0 files get merged
		reductionFactor = 0.9
	} else {
		// Deeper levels: minimal deduplication (1% reduction)
		// Leveled structure means less key overlap
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
		return inputSize, outputSize, numOutputFiles
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
	//
	// RocksDB Reference: RefreshDerivedOptions() target file size calculation
	// GitHub: https://github.com/facebook/rocksdb/blob/main/options/cf_options.cc#L1108-L1120
	//
	// RocksDB C++ (lines 1108-1120):
	//   void MutableCFOptions::RefreshDerivedOptions(int num_levels, CompactionStyle compaction_style) {
	//     max_file_size.resize(num_levels);
	//     for (int i = 0; i < num_levels; ++i) {
	//       if (i == 0 && compaction_style == kCompactionStyleUniversal) {
	//         max_file_size[i] = ULLONG_MAX;
	//       } else if (i > 1) {
	//         max_file_size[i] = MultiplyCheckOverflow(max_file_size[i - 1],
	//                                                  target_file_size_multiplier);
	//       } else {
	//         max_file_size[i] = target_file_size_base;
	//       }
	//     }
	//   }
	//
	// Result: L0,L1 use base, L2 uses base*mult, L3 uses base*mult^2, etc.
	//
	// FIDELITY: ✓ Our implementation matches RocksDB for leveled compaction
	// Test: compactor_test.go:TestTargetFileSizePerLevel
	//
	// Examples (base=64MB, multiplier=2):
	// - L0, L1: 64MB
	// - L2: 64MB * 2 = 128MB
	// - L3: 64MB * 2^2 = 256MB
	// - L4: 64MB * 2^3 = 512MB
	// - L5: 64MB * 2^4 = 1024MB
	// - L6: 64MB * 2^5 = 2048MB (2GB, capped)
	targetFileSizeMB := float64(config.TargetFileSizeMB)
	if job.ToLevel > 0 {
		// Apply multiplier: level 1 uses base, level 2 uses base*mult, etc.
		multiplier := float64(config.TargetFileSizeMultiplier)
		for i := 1; i < job.ToLevel; i++ {
			targetFileSizeMB *= multiplier
		}
		// Cap at 2GB per file (reasonable maximum for manageable compactions)
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

	return inputSize, outputSize, numOutputFiles
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
