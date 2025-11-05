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
		// FIDELITY: ✓ Matches RocksDB behavior - levels below base_level (in dynamic mode)
		// have target = 0 and should not be scored (they're unnecessary)
		// RocksDB Reference: VersionStorageInfo::CalculateBaseBytes() - levels below base_level
		// are marked as unnecessary and not compacted
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

// calculateBaseLevel computes the base level for dynamic level bytes mode
//
// RocksDB Reference: VersionStorageInfo::CalculateBaseBytes() lines 4918-4944
// GitHub: https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L4918-L4944
//
// RocksDB C++ (lines 4918-4944):
//
//	```cpp
//	int first_non_empty_level = -1;
//	// Find size of non-L0 level of most data.
//	// Cannot use the size of the last level because it can be empty or less
//	// than previous levels after compaction.
//	for (int i = 1; i < num_levels_; i++) {
//	  uint64_t total_size = 0;
//	  for (const auto& f : files_[i]) {
//	    total_size += f->fd.GetFileSize();
//	  }
//	  if (total_size > 0 && first_non_empty_level == -1) {
//	    first_non_empty_level = i;
//	  }
//	}
//	if (max_level_size == 0) {
//	  // No data for L1 and up. L0 compacts to last level directly.
//	  // No compaction from L1+ needs to be scheduled.
//	  base_level_ = num_levels_ - 1;
//	} else {
//	  base_level_ = first_non_empty_level;
//	  // ... calculate base_level_size ...
//	}
//	```
//
// calculateBaseLevel finds the base level (lowest non-empty level below L0)
//
// RocksDB Reference: VersionStorageInfo::base_level()
// Used by both:
// - Universal compaction: UniversalCompactionStyle::CalculateBaseLevel()
// - Leveled compaction with dynamic level bytes: VersionStorageInfo::CalculateBaseBytes()
//
// Base level determination:
// - Starts at deepest level (num_levels - 1) as default
// - Searches from level 1 upwards for first non-empty level
// - Base level is the lowest (shallowest) non-empty level below L0
// - Returns deepest level if all levels below L0 are empty
//
// FIDELITY: ✓ Unified implementation used by both compaction styles
// - Universal compaction: files below base level are never compacted
// - Leveled compaction with dynamic level bytes: L0 compacts directly to base level, skipping empty intermediate levels
//
// NOTE: For leveled compaction with dynamic level bytes, use calculateDynamicBaseLevel() instead,
// which calculates base level based on max level size (not just first non-empty level).
func (t *LSMTree) calculateBaseLevel() int {
	// Start from deepest level (default if all empty)
	baseLevel := len(t.Levels) - 1

	// Find first non-empty level (starting from L1, skip L0)
	for i := 1; i < len(t.Levels); i++ {
		if t.Levels[i].FileCount > 0 || t.Levels[i].TotalSize > 0 {
			baseLevel = i
			break
		}
	}

	return baseLevel
}

// calculateDynamicBaseLevel calculates the base level for dynamic level bytes mode
// based on the max level size (not just first non-empty level).
//
// RocksDB Reference: VersionStorageInfo::CalculateBaseBytes() lines 4947-5006
// GitHub: https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L4947-L5006
//
// FIDELITY: ✓ Matches RocksDB's dynamic base level calculation
// As data grows, base level moves UP (toward L1) to create intermediate levels
//
// This implementation matches calculateLevelTargets() exactly for consistency.
func (t *LSMTree) calculateDynamicBaseLevel(config SimConfig) int {
	// Find first non-empty level and max level size
	firstNonEmptyLevel := -1
	maxLevelSize := 0.0
	for i := 1; i < len(t.Levels); i++ {
		totalSize := t.Levels[i].TotalSize
		if totalSize > 0 && firstNonEmptyLevel == -1 {
			firstNonEmptyLevel = i
		}
		if totalSize > maxLevelSize {
			maxLevelSize = totalSize
		}
	}

	// If all L1+ are empty, base level is deepest level
	if maxLevelSize == 0 {
		return len(t.Levels) - 1
	}

	// Calculate base level based on max level size
	// Use the same algorithm as calculateLevelTargets for consistency
	baseBytesMax := float64(config.MaxBytesForLevelBaseMB)
	baseBytesMin := baseBytesMax / float64(config.LevelMultiplier)
	curLevelSize := maxLevelSize

	// Work backwards from last level to first_non_empty_level
	// This calculates what curLevelSize would be for first_non_empty_level
	// (matching the loop in calculateLevelTargets)
	for i := len(t.Levels) - 2; i >= firstNonEmptyLevel; i-- {
		curLevelSize = curLevelSize / float64(config.LevelMultiplier)
	}

	baseLevel := firstNonEmptyLevel

	if curLevelSize <= baseBytesMin {
		// Case 1: Target size of first non-empty level would be < base_bytes_min
		// Base level stays at first non-empty level
		baseLevel = firstNonEmptyLevel
	} else {
		// Case 2: Find base level by working backwards
		// RocksDB lines 4993-4998
		// Use curLevelSize (already calculated for first_non_empty_level) in the WHILE loop
		for baseLevel > 1 && curLevelSize > baseBytesMax {
			baseLevel--
			curLevelSize = curLevelSize / float64(config.LevelMultiplier)
		}
	}

	return baseLevel
}

// calculateLevelTargets computes target sizes for each level
//
// RocksDB Reference: VersionStorageInfo::CalculateBaseBytes()
// GitHub: https://github.com/facebook/rocksdb/blob/main/db/version_set.cc#L3074-L3205
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
//	  // ... (see implementation below for full algorithm)
//	}
//	```
//
// FIDELITY: ✓ Static mode matches RocksDB's simple exponential sizing
// FIDELITY: ✓ Dynamic mode matches RocksDB's CalculateBaseBytes() algorithm (lines 4915-5023)
//
// Dynamic mode algorithm (from RocksDB lines 4915-5023):
// 1. Find first_non_empty_level (base_level candidate)
// 2. Find max_level_size (largest level with data)
// 3. Work backwards from last level to calculate appropriate sizes
// 4. Determine base_level and base_level_size
// 5. Calculate targets for each level >= base_level
// 6. Levels below base_level have target = 0 (unnecessary)
// 7. Ensure levels >= base_level have target >= base_bytes_max (prevent hourglass)
func (t *LSMTree) calculateLevelTargets(config SimConfig) []float64 {
	targets := make([]float64, len(t.Levels))

	if config.LevelCompactionDynamicLevelBytes {
		// FIDELITY: ✓ Matches RocksDB CalculateBaseBytes() dynamic mode (lines 4915-5023)
		// RocksDB Reference: db/version_set.cc#L4915-L5023
		//
		// RocksDB C++ (lines 4915-5023):
		//
		//	```cpp
		//	assert(ioptions.compaction_style == kCompactionStyleLevel);
		//	uint64_t max_level_size = 0;
		//	int first_non_empty_level = -1;
		//	for (int i = 1; i < num_levels_; i++) {
		//	  uint64_t total_size = 0;
		//	  for (const auto& f : files_[i]) {
		//	    total_size += f->fd.GetFileSize();
		//	  }
		//	  if (total_size > 0 && first_non_empty_level == -1) {
		//	    first_non_empty_level = i;
		//	  }
		//	  if (total_size > max_level_size) {
		//	    max_level_size = total_size;
		//	  }
		//	}
		//
		//	// Prefill every level's max bytes to disallow compaction from there.
		//	for (int i = 0; i < num_levels_; i++) {
		//	  level_max_bytes_[i] = std::numeric_limits<uint64_t>::max();
		//	}
		//
		//	if (max_level_size == 0) {
		//	  base_level_ = num_levels_ - 1;
		//	} else {
		//	  uint64_t base_bytes_max = options.max_bytes_for_level_base;
		//	  uint64_t base_bytes_min = base_bytes_max / options.max_bytes_for_level_multiplier;
		//	  uint64_t cur_level_size = max_level_size;
		//	  // Work backwards to find base_level
		//	  for (int i = num_levels_ - 2; i >= first_non_empty_level; i--) {
		//	    cur_level_size = cur_level_size / options.max_bytes_for_level_multiplier;
		//	    // ... determine base_level and base_level_size ...
		//	  }
		//	  uint64_t level_size = base_level_size;
		//	  for (int i = base_level_; i < num_levels_; i++) {
		//	    if (i > base_level_) {
		//	      level_size = level_size * level_multiplier_;
		//	    }
		//	    level_max_bytes_[i] = std::max(level_size, base_bytes_max);
		//	  }
		//	}
		//	```

		// Step 1: Find first non-empty level and max level size
		firstNonEmptyLevel := -1
		maxLevelSize := 0.0
		for i := 1; i < len(t.Levels); i++ {
			totalSize := t.Levels[i].TotalSize
			if totalSize > 0 && firstNonEmptyLevel == -1 {
				firstNonEmptyLevel = i
			}
			if totalSize > maxLevelSize {
				maxLevelSize = totalSize
			}
		}

		// Step 2: Initialize all targets to 0 (will be set for levels >= base_level)
		for i := 0; i < len(targets); i++ {
			targets[i] = 0
		}

		// Step 3: Handle case where all L1+ are empty
		if maxLevelSize == 0 {
			// No data for L1 and up. L0 compacts to last level directly.
			// Base level is deepest level, but we don't set targets (they're all 0)
			// L0 will compact directly to last level
			targets[0] = float64(config.MaxBytesForLevelBaseMB) // L0 uses file count, not size
			return targets
		}

		// Step 4: Calculate base_level and base_level_size
		// RocksDB lines 4947-5006
		baseBytesMax := float64(config.MaxBytesForLevelBaseMB)
		baseBytesMin := baseBytesMax / float64(config.LevelMultiplier)
		curLevelSize := maxLevelSize

		// Work backwards from last level to first_non_empty_level
		// Find lowest unnecessary level (if any)
		lowestUnnecessaryLevel := -1
		for i := len(t.Levels) - 2; i >= firstNonEmptyLevel; i-- {
			curLevelSize = curLevelSize / float64(config.LevelMultiplier)
			if lowestUnnecessaryLevel == -1 && curLevelSize <= baseBytesMin {
				lowestUnnecessaryLevel = i
			}
		}

		// Determine base_level and base_level_size
		baseLevel := firstNonEmptyLevel
		var baseLevelSize float64

		if curLevelSize <= baseBytesMin {
			// Case 1: Target size of first non-empty level would be < base_bytes_min
			// Set base_level_size to base_bytes_min + 1
			baseLevelSize = baseBytesMin + 1.0
			baseLevel = firstNonEmptyLevel
		} else {
			// Case 2: Find base level by working backwards
			// RocksDB lines 4993-4998
			// CRITICAL: curLevelSize here is already the calculated size for first_non_empty_level
			// from the loop above. We use it directly in the WHILE loop.
			baseLevel = firstNonEmptyLevel

			// FIDELITY: ✓ Matches RocksDB lines 4993-4998 exactly
			// while (base_level_ > 1 && cur_level_size > base_bytes_max) {
			//   --base_level_;
			//   cur_level_size = cur_level_size / multiplier;
			// }
			// Note: cur_level_size here is already the size for first_non_empty_level
			for baseLevel > 1 && curLevelSize > baseBytesMax {
				baseLevel--
				curLevelSize = curLevelSize / float64(config.LevelMultiplier)
			}

			// Recalculate cur_level_size for base_level (for base_level_size calculation)
			curLevelSize = maxLevelSize
			for i := len(t.Levels) - 2; i >= baseLevel; i-- {
				curLevelSize = curLevelSize / float64(config.LevelMultiplier)
			}

			if curLevelSize > baseBytesMax {
				// Even L1 will be too large
				baseLevelSize = baseBytesMax
			} else {
				baseLevelSize = math.Max(1.0, curLevelSize)
			}
		}

		// Step 5: Calculate targets for levels >= base_level
		// RocksDB lines 5011-5021
		levelSize := baseLevelSize
		for i := baseLevel; i < len(t.Levels); i++ {
			if i > baseLevel {
				levelSize = levelSize * float64(config.LevelMultiplier)
			}
			// Don't set any level below base_bytes_max. Otherwise, the LSM can
			// assume an hourglass shape where L1+ sizes are smaller than L0.
			// This causes compaction scoring, which depends on level sizes, to favor L1+
			// at the expense of L0, which may fill up and stall.
			targets[i] = math.Max(levelSize, baseBytesMax)
		}

		// L0 uses file count, not size (but we keep this for UI display)
		targets[0] = float64(config.MaxBytesForLevelBaseMB)
	} else {
		// Static mode: simple exponential sizing (classic RocksDB behavior)
		// RocksDB Reference: db/version_set.cc#L4898-L4913
		//
		// RocksDB C++ (lines 4898-4913):
		//
		//	```cpp
		//	base_level_ = (ioptions.compaction_style == kCompactionStyleLevel) ? 1 : -1;
		//	for (int i = 0; i < ioptions.num_levels; ++i) {
		//	  if (i == 0 && ioptions.compaction_style == kCompactionStyleUniversal) {
		//	    level_max_bytes_[i] = options.max_bytes_for_level_base;
		//	  } else if (i > 1) {
		//	    level_max_bytes_[i] = MultiplyCheckOverflow(
		//	        MultiplyCheckOverflow(level_max_bytes_[i - 1],
		//	                              options.max_bytes_for_level_multiplier),
		//	        options.MaxBytesMultiplerAdditional(i - 1));
		//	  } else {
		//	    level_max_bytes_[i] = options.max_bytes_for_level_base;
		//	  }
		//	}
		//	```
		//
		// FIDELITY: ✓ Matches RocksDB's static mode exactly
		// L1 = base, L2 = base * multiplier, L3 = base * multiplier^2, etc.
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
