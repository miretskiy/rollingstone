package simulator

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

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
	fileSelectDist    filePicker   // For picking files from source level
	overlapSelectDist filePicker   // For estimating overlaps in target level
	rng               *rand.Rand   // Random number generator for file selection
	activeCompactions map[int]bool // Track levels currently being compacted
}

// NewLeveledCompactor creates a compactor with default distributions
// Distribution choices model typical RocksDB workload characteristics:
// - Geometric for file selection: usually pick 1-2 files, occasionally more
// - Geometric for overlaps: better balance than Exponential (less extreme bias toward 1 file)
// If seed is 0, uses a time-based random seed
func NewLeveledCompactor(seed int64) *LeveledCompactor {
	var rng *rand.Rand
	if seed == 0 {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	} else {
		rng = rand.New(rand.NewSource(seed))
	}

	return &LeveledCompactor{
		fileSelectDist:    newDistributionAdapter(DistGeometric), // Favor picking fewer files
		overlapSelectDist: newDistributionAdapter(DistGeometric), // Geometric better than Exponential for overlaps
		rng:               rng,
		activeCompactions: make(map[int]bool),
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

// PickCompaction selects the best compaction from the LSM state
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
//
// This method does fast checks first (level selection, thresholds) then picks files
func (c *LeveledCompactor) PickCompaction(lsm *LSMTree, config SimConfig) *CompactionJob {
	// Fast path: Find best level to compact (moved from FindLevelToCompact)
	// Calculate total_downcompact_bytes for accurate scoring
	totalDowncompactBytes := calculateTotalDowncompactBytes(lsm, config)

	type levelScore struct {
		level int
		score float64
	}

	// Calculate scores for all levels
	// FIDELITY: ✓ In dynamic mode, only score levels >= base_level
	// RocksDB Reference: VersionStorageInfo::ComputeCompactionScore() - levels below base_level
	// have target = 0, so score = 0 (they're unnecessary)
	// GitHub: https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L3207-L3305
	baseLevel := 1 // Default for static mode
	if config.LevelCompactionDynamicLevelBytes {
		// FIDELITY: ✓ Use dynamic base level calculation based on max level size
		// As data grows, base level moves UP (toward L1) to create intermediate levels
		baseLevel = lsm.calculateDynamicBaseLevel(config)
	}

	scores := make([]levelScore, 0, len(lsm.Levels))
	for i := 0; i < len(lsm.Levels)-1; i++ {
		// In dynamic mode, skip levels below base_level (they're unnecessary)
		if config.LevelCompactionDynamicLevelBytes && i > 0 && i < baseLevel {
			continue
		}
		score := lsm.calculateCompactionScore(i, config, totalDowncompactBytes)
		scores = append(scores, levelScore{level: i, score: score})
	}

	// Sort by score descending (highest score first)
	sort.Slice(scores, func(i, j int) bool {
		return scores[j].score < scores[i].score // Descending order
	})

	// Find first eligible level (not already compacting, target not too busy, score > threshold)
	bestLevel := -1
	for _, ls := range scores {
		// Skip if source level is already compacting
		if c.activeCompactions[ls.level] {
			continue
		}

		// FIDELITY: ⚠️ SIMPLIFIED - Target level contention check
		//
		// RocksDB Reference: CompactionPicker::FilesRangeOverlapWithCompaction()
		// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker.cc#L277-L305
		//
		// RocksDB C++ (lines 277-305):
		//   ```cpp
		//   bool CompactionPicker::RangeOverlapWithCompaction(
		//       const Slice& smallest_user_key, const Slice& largest_user_key,
		//       int level) const {
		//     const Comparator* ucmp = icmp_->user_comparator();
		//     for (Compaction* c : compactions_in_progress_) {
		//       if (c->output_level() == level &&
		//           ucmp->Compare(c->GetLargestUserKey(), smallest_user_key) > 0 &&
		//           ucmp->Compare(c->GetSmallestUserKey(), largest_user_key) < 0) {
		//         return true;  // Overlaps!
		//       }
		//     }
		//     return false;
		//   }
		//   ```
		//
		// RocksDB prevents starting a new compaction if key ranges overlap with
		// in-progress compactions at the target level. We simulate this WITHOUT
		// tracking actual keys by using statistical "resource contention":
		//
		// - Track how many files at target level are being compacted (TargetCompactingFiles)
		// - Don't start if >50% of target level files are already busy
		// - This simulates the worst-case where our exponential distribution
		//   picks many overlapping files (high contention scenario)
		//
		// FIDELITY: ✓ Behavior matches RocksDB's overlap check in spirit
		// FIDELITY: ⚠️ Uses statistical approximation instead of key-range tracking
		// The 50% threshold is a heuristic - RocksDB's actual logic is binary (overlap or not)
		targetLevelIdx := ls.level + 1
		if targetLevelIdx < len(lsm.Levels) {
			targetLevel := lsm.Levels[targetLevelIdx]
			if targetLevel.FileCount > 0 && targetLevel.TargetCompactingFiles > 0 {
				contentionRatio := float64(targetLevel.TargetCompactingFiles) / float64(targetLevel.FileCount)
				if contentionRatio > 0.5 {
					continue // Target level too busy
				}
			}
		}

		// FIDELITY: ⚠️ SIMPLIFIED - Dynamic threshold based on target level state
		//
		// RocksDB Reference: LevelCompactionBuilder compaction selection logic
		// RocksDB doesn't use explicit thresholds (2.0, 1.5) but is more conservative
		// when compacting into empty levels. RocksDB's logic:
		//
		// - If output level is empty: Requires more data before compacting (conservative)
		// - If output level has few files: Also more conservative
		//
		// Our simulation approximates this with:
		// - threshold = 2.0 when target level is empty (requires 2x over target before compacting)
		// - threshold = 1.5 when target level has < 3 files (requires 1.5x over target)
		// - threshold = 1.0 otherwise (normal compaction when over target)
		//
		// FIDELITY: ✓ Matches RocksDB's conservative behavior in spirit
		// FIDELITY: ⚠️ Constants (2.0, 1.5, 3) are simulation heuristics, not from RocksDB source
		// These values prevent premature compaction into empty/under-populated levels
		threshold := 1.0
		// For L0, check target level (base_level in dynamic mode, L1 in static mode)
		if ls.level == 0 {
			baseLevel := 1 // Default for static mode
			if config.LevelCompactionDynamicLevelBytes {
				baseLevel = lsm.calculateDynamicBaseLevel(config)
			}
			targetLevelIdx := baseLevel
			if targetLevelIdx < len(lsm.Levels) {
				targetLevel := lsm.Levels[targetLevelIdx]
				if targetLevel.FileCount == 0 {
					threshold = 2.0 // Conservative: require 2x over target before compacting into empty level
				} else if targetLevel.FileCount < 3 {
					threshold = 1.5 // Moderate: require 1.5x over target when target has few files
				}
			}
		} else if ls.level > 0 {
			targetLevelIdx := ls.level + 1
			if targetLevelIdx < len(lsm.Levels) {
				targetLevel := lsm.Levels[targetLevelIdx]
				if targetLevel.FileCount == 0 {
					threshold = 2.0 // Conservative: require 2x over target before compacting into empty level
				} else if targetLevel.FileCount < 3 {
					threshold = 1.5 // Moderate: require 1.5x over target when target has few files
				}
			}
		}

		if ls.score > threshold {
			bestLevel = ls.level
			break // Found eligible level, stop searching
		}
	}

	// Fast path: No level needs compaction
	if bestLevel < 0 {
		return nil
	}

	// Now pick files for the selected level
	level := bestLevel
	sourceLevel := lsm.Levels[level]

	// Mark this level as compacting
	c.activeCompactions[level] = true

	if level == 0 {
		// FIDELITY: ✓ In dynamic mode, L0 compacts to base_level (not always L1)
		// RocksDB Reference: LevelCompactionBuilder::PickCompaction() line 187
		// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_level.cc#L187
		//
		// RocksDB C++ (line 187):
		//   ```cpp
		//   output_level_ = (start_level_ == 0) ? vstorage_->base_level() : start_level_ + 1;
		//   ```
		//
		// In static mode: base_level = 1, so L0→L1
		// In dynamic mode: base_level can be L1, L2, L3, etc. (depending on data distribution)
		// L0 compacts directly to base_level, skipping empty intermediate levels
		baseLevel := 1 // Default for static mode
		if config.LevelCompactionDynamicLevelBytes {
			baseLevel = lsm.calculateDynamicBaseLevel(config)
		}

		// Always prefer L0→base_level compaction (normal case)
		// Intra-L0 is a FALLBACK for extreme cases only
		targetLevel := lsm.Levels[baseLevel]

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
			ToLevel:     baseLevel,         // L0→base_level (dynamic mode) or L0→L1 (static mode)
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

	// Clear active compaction tracking when compaction completes
	defer func() {
		delete(c.activeCompactions, job.FromLevel)
	}()

	// Handle subcompactions: execute each subcompaction in parallel
	//
	// RocksDB Reference: CompactionJob::RunSubcompactions()
	// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_job.cc#L710-L735
	//
	// RocksDB C++ (lines 710-735):
	//
	//	void CompactionJob::RunSubcompactions() {
	//	  const size_t num_threads = compact_->sub_compact_states.size();
	//	  assert(num_threads > 0);
	//	  // Launch a thread for each of subcompactions 1...num_threads-1
	//	  std::vector<port::Thread> thread_pool;
	//	  thread_pool.reserve(num_threads - 1);
	//	  for (size_t i = 1; i < compact_->sub_compact_states.size(); i++) {
	//	    thread_pool.emplace_back(&CompactionJob::ProcessKeyValueCompaction, this,
	//	                             &compact_->sub_compact_states[i]);
	//	  }
	//	  // Always schedule the first subcompaction (whether or not there are also
	//	  // others) in the current thread to be efficient with resources
	//	  ProcessKeyValueCompaction(compact_->sub_compact_states.data());
	//	  // Wait for all other threads (if there are any) to finish execution
	//	  for (auto& thread : thread_pool) {
	//	    thread.join();
	//	  }
	//	}
	//
	// FIDELITY: ✓ Matches RocksDB's parallel execution model
	// ⚠️ SIMPLIFIED: Subcompactions execute sequentially in simulation (modeled as parallel)
	// - RocksDB: truly parallel threads
	// - Simulator: execute sequentially but aggregate results as if parallel
	// - Duration calculated as max(subcompaction durations) in scheduling code
	if len(job.Subcompactions) > 0 {
		// Execute each subcompaction independently
		// All subcompactions run in parallel (modeled by max duration in scheduling)
		for _, subcompaction := range job.Subcompactions {
			// Create a temporary CompactionJob for this subcompaction
			subJob := &CompactionJob{
				FromLevel:   job.FromLevel,
				ToLevel:     job.ToLevel,
				SourceFiles: subcompaction.SourceFiles,
				TargetFiles: subcompaction.TargetFiles,
				IsIntraL0:   job.IsIntraL0,
			}

			// Execute this subcompaction (recurse, but without subcompactions to avoid infinite loop)
			subInput, subOutput, subFileCount := c.executeCompactionSingle(subJob, lsm, config, virtualTime)
			inputSize += subInput
			outputSize += subOutput
			outputFileCount += subFileCount
		}

		fmt.Printf("[SUBPCOMPACTION] L%d→L%d: Executed %d subcompactions, total input=%.1fMB, output=%.1fMB, %d files\n",
			job.FromLevel, job.ToLevel, len(job.Subcompactions), inputSize, outputSize, outputFileCount)
		return inputSize, outputSize, outputFileCount
	}

	// Single compaction (no subcompactions) - execute normally
	return c.executeCompactionSingle(job, lsm, config, virtualTime)
}

// executeCompactionSingle performs a single compaction (without subcompactions)
// This is the core compaction logic extracted from ExecuteCompaction
func (c *LeveledCompactor) executeCompactionSingle(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64, outputFileCount int) {
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
	// CRITICAL: Trivial move requires that source files are NOT already in target level
	// If source files include files from target level, this is NOT a trivial move
	// (it's a real compaction that merges files)
	//
	// FIDELITY: ✓ Optimization implemented correctly
	// Test: compactor_test.go:TestTrivialMove
	if len(job.TargetFiles) == 0 && !job.IsIntraL0 {
		// Check if any source files are already in target level
		// If so, this is NOT a trivial move (it's merging files from same level)
		targetLevel := lsm.Levels[job.ToLevel]
		hasFilesFromTargetLevel := false
		for _, sourceFile := range job.SourceFiles {
			for _, targetFile := range targetLevel.Files {
				if sourceFile == targetFile {
					hasFilesFromTargetLevel = true
					break
				}
			}
			if hasFilesFromTargetLevel {
				break
			}
		}

		// Only do trivial move if no source files are in target level
		if !hasFilesFromTargetLevel {
			// Calculate input size for metrics
			for _, f := range job.SourceFiles {
				inputSize += f.SizeMB
			}

			fmt.Printf("[TRIVIAL MOVE] L%d→L%d: Moving %d files (%.1f MB) without rewriting\n",
				job.FromLevel, job.ToLevel, len(job.SourceFiles), inputSize)

			// Trivial move: output = input (no reduction)
			outputSize = inputSize
			outputFileCount = len(job.SourceFiles) // Just moving existing files

			// Remove from source level (single level for trivial move)
			lsm.Levels[job.FromLevel].removeFiles(job.SourceFiles)

			// Add all files to target level (trivial move: just move files)
			for _, f := range job.SourceFiles {
				lsm.Levels[job.ToLevel].AddFile(f)
			}

			return inputSize, outputSize, outputFileCount
		}
		// Fall through to normal compaction if source files include target level files
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
	if job.FromLevel == 0 {
		// L0→base_level: significant deduplication (10% reduction)
		// Multiple versions of same key across L0 files get merged
		// FIDELITY: ⚠️ SIMPLIFIED - Uses same reduction factor for L0→any level
		// In practice, L0→L1 has more dedup than L0→L5, but we approximate with single factor
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

	// CRITICAL BUG FIX: Universal compaction can pick files from MULTIPLE levels (e.g., L0 + L5 for size amplification)
	// We must remove files from EACH level that contributed files, not just job.FromLevel!
	// Since we know which levels contributed (from pickedRuns), we can remove files directly from those levels
	// But we don't have access to pickedRuns here, so we need to group files by level
	// Group source files by level by checking which level each file belongs to
	sourceFilesByLevel := make(map[int][]*SSTFile)
	for _, f := range job.SourceFiles {
		// Find which level this file belongs to by checking all levels
		found := false
		for level := 0; level < len(lsm.Levels); level++ {
			// Check if this file pointer exists in this level's Files slice
			for _, levelFile := range lsm.Levels[level].Files {
				if levelFile == f {
					sourceFilesByLevel[level] = append(sourceFilesByLevel[level], f)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			// File not found - might have been removed already or file pointer doesn't match
			// This can happen if files were modified between picking and execution
			// For now, log warning but continue - the file might have already been removed
			fmt.Printf("[WARNING] Source file %s not found in any level during removal - may have been removed already\n", f.ID)
		}
	}

	// Remove files from each source level
	for level, files := range sourceFilesByLevel {
		if len(files) > 0 {
			lsm.Levels[level].removeFiles(files)
			fmt.Printf("[COMPACTION] Removed %d files from L%d\n", len(files), level)
		}
	}

	// Remove target files from target level
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
	if l.TotalSize < 0 {
		l.TotalSize = 0
	}
}
