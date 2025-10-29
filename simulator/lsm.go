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
	Number                int        `json:"level"`
	Files                 []*SSTFile `json:"files"`
	TotalSize             float64    `json:"totalSizeMB"`
	FileCount             int        `json:"fileCount"`
	CompactingSize        float64    `json:"compactingSizeMB"`      // Size of files currently being compacted FROM this level
	CompactingFileCount   int        `json:"compactingFileCount"`   // Number of files currently being compacted FROM this level
	TargetCompactingFiles int        `json:"targetCompactingFiles"` // Number of files at this level being used as TARGET in compactions
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

// NeedsFlush returns true if memtable should be flushed based on size
//
// FIDELITY: RocksDB Reference - Memtable Flush Triggers
// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_write.cc#L1432-L1445
// https://github.com/facebook/rocksdb/blob/main/db/memtable_list.cc#L414-L428
//
// C++ snippet from DBImpl::HandleWriteBufferManagerFlush():
//
//	```cpp
//	// Size-based flush trigger
//	if (cfd->imm()->NumNotFlushed() >= cfd->ioptions()->max_write_buffer_number) {
//	  return true;  // Too many immutable memtables
//	}
//
//	// Check write buffer size
//	if (write_buffer_manager_->ShouldFlush()) {
//	  // Memtable exceeds write_buffer_size
//	  return true;
//	}
//	```
//
// FIDELITY: ✓ Size-based flush matches RocksDB's write_buffer_size check
func (t *LSMTree) NeedsFlush() bool {
	// Size-based trigger - matches RocksDB's write_buffer_size check
	return t.MemtableCurrentSize >= t.MemtableMaxSize
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
//
// FIDELITY: RocksDB Reference - Level compaction trigger logic
// https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L3207-L3260
//
// C++ snippet from VersionStorageInfo::ComputeCompactionScore():
//
//	```cpp
//	if (level == 0) {
//	  // L0: file count trigger
//	  score = static_cast<double>(num_sorted_runs) /
//	          mutable_cf_options.level0_file_num_compaction_trigger;
//	  // score > 1.0 means needs compaction
//	} else {
//	  // L1+: size-based trigger
//	  score = static_cast<double>(level_bytes_no_compacting) /
//	          MaxBytesForLevel(level);
//	  // score > 1.0 means needs compaction
//	}
//	```
//
// FIDELITY: ✓ L0 file count trigger matches RocksDB exactly
// FIDELITY: ⚠️ SIMPLIFIED - Uses TotalSize instead of level_bytes_no_compacting
//
//	(We don't exclude files being compacted here, but do in scoring)
//
// FIDELITY: ⚠️ NOTE - This function is largely UNUSED in favor of score-based
//
//	scheduling in simulator.go:tryScheduleCompaction()
func (t *LSMTree) NeedsCompaction(level int, l0Trigger int, maxBytesForLevelBase float64, multiplier int) bool {
	if level < 0 || level >= len(t.Levels) {
		return false
	}

	if level == 0 {
		// L0 uses file count trigger (matches RocksDB's level0_file_num_compaction_trigger)
		return t.Levels[0].FileCount >= l0Trigger
	}

	// L1+ use size triggers (matches RocksDB's MaxBytesForLevel check)
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
//
// FIDELITY: RocksDB Reference - VersionStorageInfo::ComputeCompactionScore()
// https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L3207-L3305
//
// C++ snippet from VersionStorageInfo::ComputeCompactionScore():
//
//	```cpp
//	if (level == 0) {
//	  // L0 scoring: max of file count score and size score
//	  int num_sorted_runs = level0_non_overlapping
//	                        ? NumLevelFiles(0)
//	                        : num_l0_sorted_run_files;
//	  score = static_cast<double>(num_sorted_runs) /
//	          mutable_cf_options.level0_file_num_compaction_trigger;
//
//	  if (compaction_style_ == kCompactionStyleLevel && num_levels() > 1) {
//	    double size_score = static_cast<double>(total) /
//	                       MaxBytesForLevel(0);
//	    score = std::max(score, size_score);
//	  }
//	} else {
//	  // L1+ scoring: size-based
//	  uint64_t level_bytes_no_compacting = 0;
//	  for (auto* f : files_[level]) {
//	    if (!f->being_compacted) {
//	      level_bytes_no_compacting += f->compensated_file_size;
//	    }
//	  }
//	  score = static_cast<double>(level_bytes_no_compacting) / MaxBytesForLevel(level);
//
//	  // Apply kScoreScale when dynamic mode and downcompact bytes present
//	  if (level_compaction_dynamic_level_bytes_ &&
//	      level_bytes_no_compacting > MaxBytesForLevel(level) &&
//	      total_downcompact_bytes > 0) {
//	    score = level_bytes_no_compacting /
//	            (MaxBytesForLevel(level) + total_downcompact_bytes) * kScoreScale;
//	  }
//	}
//	```
//
// FIDELITY: ✓ L0 scoring matches RocksDB's file count / trigger logic
// FIDELITY: ✓ L1+ uses level_bytes_no_compacting (excludes files being compacted)
// FIDELITY: ⚠️ PARTIAL - Dynamic mode with kScoreScale implemented but needs full CalculateBaseBytes()
// FIDELITY: ✗ NOT IMPLEMENTED - compensated_file_size (applies to both static & dynamic modes)
//
//	RocksDB adjusts file size based on deletion count for compaction scoring:
//	  compensated_file_size = actual_size + deletion_boost + range_tombstone_impact
//	This makes files with many deletions higher priority for compaction.
//
//	We use raw file size because we don't track individual keys/deletions.
//	Impact: Minor - we model the overall effect with CompactionReductionFactor.
func (t *LSMTree) calculateCompactionScore(level int, config SimConfig, totalDowncompactBytes float64) float64 {
	if level < 0 || level >= len(t.Levels) {
		return 0.0
	}

	levelState := t.Levels[level]

	// RocksDB's score scaling constant (db/version_set.cc)
	// Applied when accounting for incoming compactions to keep score > 1.0
	const kScoreScale = 10.0

	if level == 0 {
		// L0 score = max(fileCount / trigger, totalSize / max_bytes_for_level_base)
		// RocksDB scores L0 by file count because each file must be checked during reads
		// This matches RocksDB's level0_file_num_compaction_trigger behavior
		fileScore := float64(levelState.FileCount) / float64(config.L0CompactionTrigger)
		sizeScore := levelState.TotalSize / float64(config.MaxBytesForLevelBaseMB)
		if fileScore > sizeScore {
			return fileScore
		}
		return sizeScore
	}

	// For Ln (n >= 1): score = level_bytes_no_compacting / targetSize
	// RocksDB excludes files currently being compacted from size calculation
	// Reference: level_bytes_no_compacting calculation in ComputeCompactionScore()
	levelBytesNoCompacting := levelState.TotalSize - levelState.CompactingSize
	if levelBytesNoCompacting < 0 {
		levelBytesNoCompacting = 0 // Safety check
	}

	targets := t.calculateLevelTargets(config)
	if level >= len(targets) {
		return 0.0
	}

	targetSize := targets[level]
	if targetSize <= 0 {
		return 0.0
	}

	var score float64

	if !config.LevelCompactionDynamicLevelBytes {
		// Static mode: simple ratio (classic RocksDB behavior)
		score = levelBytesNoCompacting / targetSize
	} else {
		// TODO: Dynamic mode needs full CalculateBaseBytes() implementation
		// Currently implements partial logic from RocksDB's dynamic leveling
		// Reference: VersionStorageInfo::CalculateBaseBytes() (db/version_set.cc#L3074)
		//
		// Dynamic mode: account for incoming data from upper levels
		if levelBytesNoCompacting < targetSize {
			// Level is under target: simple ratio
			score = levelBytesNoCompacting / targetSize
		} else {
			// Level exceeds target AND has incoming compactions
			// RocksDB formula: score = size / (target + downcompact) * kScoreScale
			// This keeps score > 1.0 while deprioritizing levels with heavy incoming data
			if totalDowncompactBytes > 0 {
				score = levelBytesNoCompacting / (targetSize + totalDowncompactBytes) * kScoreScale
			} else {
				score = levelBytesNoCompacting / targetSize
			}
		}
	}

	return score
}

// calculateLevelTargets computes target sizes for each level
//
// FIDELITY: RocksDB Reference - VersionStorageInfo::CalculateBaseBytes()
// https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L3074-L3205
//
// C++ snippet from VersionStorageInfo::CalculateBaseBytes():
//
//	```cpp
//	void VersionStorageInfo::CalculateBaseBytes(const ImmutableOptions& ioptions,
//	                                              const MutableCFOptions& options) {
//	  if (!ioptions.level_compaction_dynamic_level_bytes) {
//	    // Static mode: exponential sizing
//	    base_level_ = 1;  // Start from L1
//	    level_max_bytes_[0] = 0;  // L0 not size-limited
//	    for (int level = 1; level < num_levels_; level++) {
//	      level_max_bytes_[level] = options.MaxBytesForLevel(level);
//	    }
//	    return;
//	  }
//
//	  // Dynamic mode: work backwards to ensure last level has ~90% of data
//	  uint64_t max_level_size = 0;
//	  int first_non_empty_level = -1;
//	  for (int level = num_levels_ - 1; level >= 0; level--) {
//	    if (LevelBytes(level) > 0) {
//	      if (first_non_empty_level == -1) {
//	        first_non_empty_level = level;
//	      }
//	      max_level_size = std::max(max_level_size, LevelBytes(level));
//	    }
//	  }
//
//	  uint64_t base_bytes_max = options.max_bytes_for_level_base;
//	  uint64_t base_bytes_min = base_bytes_max / options.max_bytes_for_level_multiplier;
//
//	  uint64_t cur_level_size = max_level_size;
//	  for (int level = num_levels_ - 2; level >= 0; level--) {
//	    cur_level_size = RoundUpTo(cur_level_size, 1024);
//	    cur_level_size /= options.max_bytes_for_level_multiplier;
//	    if (cur_level_size < base_bytes_min) {
//	      // Level too small, skip it (base_level moves down)
//	      level_max_bytes_[level] = 0;
//	    } else {
//	      level_max_bytes_[level] = cur_level_size;
//	    }
//	  }
//	}
//	```
//
// FIDELITY: ✓ Static mode matches RocksDB's simple exponential sizing
// FIDELITY: ✗ TODO - Dynamic mode is SIMPLIFIED (missing CalculateBaseBytes() logic)
//
//	Real RocksDB dynamic mode (CalculateBaseBytes()):
//	- Scans ACTUAL data distribution across levels at runtime
//	- Finds first non-empty level and largest level size
//	- Works backwards to determine base_level (where L0 compacts to)
//	- Marks and drains "unnecessary" intermediate levels
//	- Rounds targets to 1024-byte boundaries
//	- Ensures ~90% of data in last level
//
//	Our implementation: Static exponential sizing (doesn't adapt to data)
func (t *LSMTree) calculateLevelTargets(config SimConfig) []float64 {
	targets := make([]float64, len(t.Levels))

	if config.LevelCompactionDynamicLevelBytes {
		// TODO: Dynamic mode needs full RocksDB CalculateBaseBytes() implementation
		// Current implementation is SIMPLIFIED and doesn't match RocksDB's behavior
		// Missing:
		// - Data-aware base_level calculation (find first non-empty level)
		// - Rounding to 1024-byte boundaries
		// - Proper handling of sparse levels
		// - 90% data in last level guarantee
		//
		// For now, we use a backwards calculation from max level
		// This is NOT the same as RocksDB's algorithm!
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
		// Static mode: simple exponential sizing (classic RocksDB behavior)
		// L1 = base, L2 = base * 10, L3 = base * 100, etc.
		// This matches RocksDB's non-dynamic mode exactly
		targets[0] = float64(config.MaxBytesForLevelBaseMB) // L0 uses file count, not size
		for level := 1; level < len(t.Levels); level++ {
			targets[level] = float64(config.MaxBytesForLevelBaseMB) * math.Pow(float64(config.LevelMultiplier), float64(level-1))
		}
	}

	return targets
}

// State returns the current state for JSON serialization with target sizes
func (t *LSMTree) State(virtualTime float64, config SimConfig) map[string]interface{} {
	targets := t.calculateLevelTargets(config)

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
			"level":        level.Number,
			"totalSizeMB":  level.TotalSize,
			"targetSizeMB": targets[i],
			"fileCount":    level.FileCount,
			"files":        files,
		}
	}

	return map[string]interface{}{
		"levels":                levels,
		"memtableCurrentSizeMB": t.MemtableCurrentSize,
		"totalSizeMB":           t.TotalSizeMB,
	}
}
