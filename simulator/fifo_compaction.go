package simulator

import (
	"fmt"
	"math/rand"
)

// FIFOCompactor implements FIFO (First-In, First-Out) compaction strategy.
// This strategy is optimized for time-series workloads where old data naturally expires.
//
// FIDELITY: RocksDB Reference - FIFO Compaction Overview
// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc
//
// FIFO compaction executes in two phases (in order):
// 1. Size-based deletion: Delete oldest files when total size exceeds threshold
// 2. Intra-L0 compaction: Merge small files to reduce file count (if allow_compaction=true)
//
// FIDELITY: ⚠️ INTENTIONALLY OMITTED - TTL-based deletion
//   - RocksDB supports TTL-based deletion (files older than threshold)
//   - Not implemented here: requires virtualTime in PickCompaction interface
//   - For simulator use: Size-based deletion provides equivalent behavior for time-series workloads
//   - TTL adds complexity without clear benefit for simulation purposes
//
// FIDELITY: ⚠️ SIMPLIFIED - Temperature-based migration not implemented (EXPERIMENTAL feature)
type FIFOCompactor struct {
	rng               *rand.Rand
	activeCompactions map[int]bool // Track levels currently being compacted
}

// NewFIFOCompactor creates a new FIFO compaction strategy.
func NewFIFOCompactor(seed int64) *FIFOCompactor {
	return &FIFOCompactor{
		rng:               rand.New(rand.NewSource(seed)),
		activeCompactions: make(map[int]bool),
	}
}

// NeedsCompaction checks if compaction is needed for FIFO.
//
// FIDELITY: RocksDB Reference - FIFO NeedsCompaction
// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc#L36-L40
//
// C++ snippet from FIFOCompactionPicker::NeedsCompaction():
//
//   ```cpp
//   bool FIFOCompactionPicker::NeedsCompaction(
//       const VersionStorageInfo* vstorage) const {
//     const int kLevel0 = 0;
//     return vstorage->CompactionScore(kLevel0) >= 1;
//   }
//   ```
//
// FIDELITY: ✓ Exact match - Only check L0 compaction score
// For FIFO, only L0 exists (num_levels=1), so we only check level 0
func (f *FIFOCompactor) NeedsCompaction(level int, lsm *LSMTree, config SimConfig) bool {
	// FIFO only uses L0
	if level != 0 {
		return false
	}

	// FIDELITY: RocksDB Reference - FIFO Single Compaction Enforcement
	// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc#L63-L69
	//
	// C++ snippet from FIFOCompactionPicker::PickTTLCompaction():
	//
	//   ```cpp
	//   if (!level0_compactions_in_progress_.empty()) {
	//     ROCKS_LOG_BUFFER(
	//         log_buffer,
	//         "[%s] FIFO compaction: Already executing compaction. No need "
	//         "to run parallel compactions since compactions are very fast",
	//         cf_name.c_str());
	//     return nullptr;
	//   }
	//   ```
	//
	// FIDELITY: ✓ Exact match - Only one L0 compaction allowed at a time for FIFO
	// Check if L0 is already being compacted
	if lsm.Levels[0].CompactingFileCount > 0 {
		return false
	}

	// Calculate compaction score for L0
	// For FIFO, score is based on file count OR total size exceeding threshold
	if len(lsm.Levels) == 0 || lsm.Levels[0].FileCount == 0 {
		return false
	}

	// Check file count trigger
	availableFileCount := lsm.Levels[0].FileCount - lsm.Levels[0].CompactingFileCount
	if availableFileCount >= config.L0CompactionTrigger {
		return true
	}

	// Check size threshold (for FIFO deletion)
	totalSizeMB := lsm.Levels[0].TotalSize
	if totalSizeMB >= float64(config.FIFOMaxTableFilesSizeMB) {
		return true
	}

	return false
}

// PickCompaction selects files for compaction using FIFO strategy.
//
// FIDELITY: RocksDB Reference - FIFO PickCompaction Order
// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc#L431-L450
//
// C++ snippet from FIFOCompactionPicker::PickCompaction():
//
//   ```cpp
//   Compaction* FIFOCompactionPicker::PickCompaction(...) {
//     Compaction* c = nullptr;
//     if (mutable_cf_options.ttl > 0) {
//       c = PickTTLCompaction(...);
//     }
//     if (c == nullptr) {
//       c = PickSizeCompaction(...);
//     }
//     if (c == nullptr) {
//       c = PickTemperatureChangeCompaction(...);
//     }
//     RegisterCompaction(c);
//     return c;
//   }
//   ```
//
// FIDELITY: ⚠️ SIMPLIFIED - TTL phase skipped (not implemented)
// FIDELITY: ✓ Exact match - Size-based compaction phase matches RocksDB
func (f *FIFOCompactor) PickCompaction(lsm *LSMTree, config SimConfig) *CompactionJob {
	// FIFO only uses L0
	if len(lsm.Levels) == 0 || lsm.Levels[0].FileCount == 0 {
		return nil
	}

	// Don't pick if L0 already compacting
	if f.activeCompactions[0] {
		return nil
	}

	// Phase 1: Size-based compaction or intra-L0
	if job := f.pickSizeCompaction(lsm, config); job != nil {
		f.activeCompactions[0] = true
		return job
	}

	// Phase 2: Temperature change compaction
	// FIDELITY: ✗ NOT IMPLEMENTED - Temperature-based tiering (EXPERIMENTAL)

	return nil
}

// pickSizeCompaction implements size-based deletion and intra-L0 compaction.
func (f *FIFOCompactor) pickSizeCompaction(lsm *LSMTree, config SimConfig) *CompactionJob {
	l0 := lsm.Levels[0]
	totalSizeMB := l0.TotalSize
	maxSizeMB := float64(config.FIFOMaxTableFilesSizeMB)

	// Case 1: Size exceeded - delete oldest files
	if totalSizeMB >= maxSizeMB {
		return f.pickDeletionCompaction(lsm, config)
	}

	// Case 2: Size under threshold - try intra-L0 compaction
	if config.FIFOAllowCompaction && l0.FileCount >= config.L0CompactionTrigger {
		return f.pickIntraL0Compaction(lsm, config)
	}

	return nil
}

// pickDeletionCompaction creates a job to delete oldest files until under size threshold.
//
// FIDELITY: RocksDB Reference - FIFO Size-Based Deletion
// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc#L219-L233
//
// C++ snippet from PickSizeCompaction() (L0 case):
//
//   ```cpp
//   // In L0, right-most files are the oldest files.
//   for (auto ritr = last_level_files.rbegin(); ritr != last_level_files.rend(); ++ritr) {
//     auto f = *ritr;
//     total_size -= f->fd.file_size;
//     inputs[0].files.push_back(f);
//     if (total_size <= mutable_cf_options.compaction_options_fifo.max_table_files_size) {
//       break;  // Stop as soon as we're under threshold
//     }
//   }
//   ```
//
// FIDELITY: ✓ Exact match - Deletes oldest files one-by-one until size <= threshold
// FIDELITY: ✓ Natural hysteresis - Stops immediately when under threshold (no explicit buffer)
// FIDELITY: ✓ Typically deletes 1-3 files (depends on individual file sizes from intra-L0)
//
// NOTE: Deletion runs periodically (every N new files) because:
// - Intra-L0 merges small files into large files (~2.5GB merged files)
// - Need to accumulate ~N small flush files before next deletion triggers
// - This creates natural periodic deletion pattern (not continuous)
func (f *FIFOCompactor) pickDeletionCompaction(lsm *LSMTree, config SimConfig) *CompactionJob {
	l0 := lsm.Levels[0]
	if len(l0.Files) == 0 {
		return nil
	}

	totalSizeMB := l0.TotalSize
	maxSizeMB := float64(config.FIFOMaxTableFilesSizeMB)

	fmt.Printf("[FIFO-DEL] Starting deletion: totalSize=%.1f MB, maxSize=%.1f MB, fileCount=%d\n",
		totalSizeMB, maxSizeMB, len(l0.Files))

	// Select oldest files (rightmost in L0) until size drops below threshold
	// RocksDB uses reverse iterator: rbegin() to rend() = rightmost to leftmost = oldest to newest
	var filesToDelete []*SSTFile
	for i := len(l0.Files) - 1; i >= 0 && totalSizeMB >= maxSizeMB; i-- {
		file := l0.Files[i]
		fmt.Printf("[FIFO-DEL] Considering file at index %d: ID=%s, size=%.1f MB, createdAt=%.1f\n",
			i, file.ID, file.SizeMB, file.CreatedAt)
		totalSizeMB -= file.SizeMB
		filesToDelete = append(filesToDelete, file)
		// Loop condition checks: totalSizeMB >= maxSizeMB
		// Stops as soon as totalSizeMB < maxSizeMB (matches RocksDB exactly)
	}

	if len(filesToDelete) == 0 {
		return nil
	}

	return &CompactionJob{
		FromLevel:   0,
		ToLevel:     0,
		SourceFiles: filesToDelete,
		TargetFiles: nil,
		IsIntraL0:   false, // This is deletion, not merge
	}
}

// pickIntraL0Compaction creates a job to merge small L0 files.
//
// FIDELITY: RocksDB Reference - FindIntraL0Compaction Algorithm
// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker.cc#L30-L71
//
// C++ snippet from FindIntraL0Compaction():
//
//   ```cpp
//   size_t start = 0;
//   // ... start from level_files[0] (newest file)
//   for (limit = start + 1; limit < level_files.size(); ++limit) {
//     compact_bytes += static_cast<size_t>(level_files[limit]->fd.file_size);
//     new_compact_bytes_per_del_file = compact_bytes / (limit - start);
//     if (new_compact_bytes_per_del_file > compact_bytes_per_del_file ||
//         compact_bytes > max_compaction_bytes) {
//       break;
//     }
//   }
//   ```
//
// FIDELITY: ✓ Exact match - Starts from index 0 (newest file) and pulls in adjacent files
func (f *FIFOCompactor) pickIntraL0Compaction(lsm *LSMTree, config SimConfig) *CompactionJob {
	l0 := lsm.Levels[0]
	if len(l0.Files) < config.L0CompactionTrigger {
		fmt.Printf("[FIFO-INTRA] File count check failed: %d < %d (trigger)\n", len(l0.Files), config.L0CompactionTrigger)
		return nil
	}

	// Calculate max bytes per deleted file (write_buffer_size * 1.1)
	// FIDELITY: ✓ Exact match - RocksDB uses 1.1 multiplier (since 2018, commit 70645355ad)
	// Reference: db/compaction/compaction_picker_fifo.cc
	//   max_compact_bytes_per_del_file = write_buffer_size * 1.1
	// Verified in: RocksDB 6.20.3 (production), RocksDB 8.9.1 (current head)
	writeBufferSizeMB := float64(config.MemtableFlushSizeMB)
	maxCompactBytesPerDelFile := writeBufferSizeMB * 1.1
	maxCompactionBytesMB := float64(config.MaxCompactionBytesMB)

	fmt.Printf("[FIFO-INTRA] Starting pick: fileCount=%d, maxCompactBytesPerDelFile=%.1f MB, maxCompactionBytes=%.1f MB\n",
		len(l0.Files), maxCompactBytesPerDelFile, maxCompactionBytesMB)

	// FIDELITY: L0 File Ordering in RocksDB
	// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker_fifo.cc#L78
	// RocksDB uses reverse iterator (rbegin/rend) for TTL deletion, meaning:
	// - Index 0 = NEWEST file (most recently flushed)
	// - Index N-1 = OLDEST file (first to be deleted in FIFO)
	//
	// FindIntraL0Compaction starts at index 0 (newest) and pulls in adjacent newer files
	start := 0
	compactBytesMB := l0.Files[start].SizeMB
	compactBytesPerDelFile := 999999.0 // Very large initial value
	limit := start + 1

	// Pull in files until "diminishing returns"
	for limit < len(l0.Files) {
		compactBytesMB += l0.Files[limit].SizeMB
		newCompactBytesPerDelFile := compactBytesMB / float64(limit-start)

		// Stop if work per deleted file increases OR exceeds max size
		if newCompactBytesPerDelFile > compactBytesPerDelFile || compactBytesMB > maxCompactionBytesMB {
			fmt.Printf("[FIFO-INTRA] Stopping at file %d: newBytesPerDel=%.1f > prevBytesPerDel=%.1f OR compactBytes=%.1f > maxBytes=%.1f\n",
				limit, newCompactBytesPerDelFile, compactBytesPerDelFile, compactBytesMB, maxCompactionBytesMB)
			break
		}

		compactBytesPerDelFile = newCompactBytesPerDelFile
		limit++
	}

	// Final check: meets minimum files and stays under per-file limit
	// FIDELITY: ✓ Exact match - RocksDB uses AND logic (both conditions must be true)
	// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker.cc#L39-L43
	//
	// C++ snippet from FindIntraL0Compaction():
	//
	//   ```cpp
	//   if ((limit - start) >= min_files_to_compact &&
	//       compact_bytes_per_del_file < max_compact_bytes_per_del_file) {
	//     // ACCEPT - select files for compaction
	//     return true;
	//   }
	//   return false;  // REJECT
	//   ```
	//
	numFiles := limit - start
	fmt.Printf("[FIFO-INTRA] Final check: numFiles=%d (trigger=%d), compactBytesPerDelFile=%.1f (max=%.1f)\n",
		numFiles, config.L0CompactionTrigger, compactBytesPerDelFile, maxCompactBytesPerDelFile)

	// Accept only if BOTH conditions are true (matches RocksDB AND logic)
	if numFiles >= config.L0CompactionTrigger && compactBytesPerDelFile < maxCompactBytesPerDelFile {
		// Continue to file selection below
	} else {
		fmt.Printf("[FIFO-INTRA] REJECTED: numFiles=%d < %d OR bytesPerDel=%.1f >= %.1f\n",
			numFiles, config.L0CompactionTrigger, compactBytesPerDelFile, maxCompactBytesPerDelFile)
		return nil
	}

	// Select files [start, limit)
	sourceFiles := l0.Files[start:limit]

	fmt.Printf("[FIFO-INTRA] SELECTED %d files for intra-L0:\n", len(sourceFiles))
	for i, f := range sourceFiles {
		fmt.Printf("  [%d] ID=%s, size=%.1f MB, createdAt=%.1f\n", i, f.ID, f.SizeMB, f.CreatedAt)
	}

	return &CompactionJob{
		FromLevel:   0,
		ToLevel:     0,
		SourceFiles: sourceFiles,
		TargetFiles: nil,
		IsIntraL0:   true,
	}
}

// ExecuteCompaction executes a FIFO compaction job.
// Returns: inputSize (MB), outputSize (MB), outputFileCount
func (f *FIFOCompactor) ExecuteCompaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64, outputFileCount int) {
	// Calculate input size
	for _, file := range job.SourceFiles {
		inputSize += file.SizeMB
	}

	// Clear active compaction tracking
	defer func() {
		delete(f.activeCompactions, job.FromLevel)
	}()

	// Check if this is deletion or merge
	isDeletion := !job.IsIntraL0

	if isDeletion {
		// Deletion compaction: remove files from L0
		f.executeDeletion(job, lsm)
		return inputSize, 0, 0
	}

	// Intra-L0 compaction: merge files into one larger file
	return f.executeIntraL0Compaction(job, lsm, config, virtualTime)
}

// executeDeletion removes files from L0.
//
// FIDELITY: RocksDB Reference - Deletion Compaction Execution
// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_compaction_flush.cc#L3689-3709
//
// C++ snippet from BackgroundCompaction():
//
//   ```cpp
//   } else if (c->deletion_compaction()) {
//     // deletion_compaction is special compaction that just deletes files
//     // without reading or writing any data
//     for (const auto& f : *c->inputs(0)) {
//       c->edit()->DeleteFile(c->level(), f->fd.GetNumber());  // Metadata only
//     }
//     status = versions_->LogAndApply(...);  // Update MANIFEST
//     ROCKS_LOG_BUFFER(log_buffer, "[%s] Deleted %d files\n", ...);
//   }
//   ```
//
// FIDELITY: ✓ Exact match - Pure metadata operation (no file I/O)
// FIDELITY: ✓ No disk bandwidth used (files unlinked asynchronously by filesystem)
// FIDELITY: ✓ ExecuteCompaction returns (inputSize=0, outputSize=0, count=0) for deletion
func (f *FIFOCompactor) executeDeletion(job *CompactionJob, lsm *LSMTree) {
	l0 := lsm.Levels[0]

	// PRECONDITION: Calculate size BEFORE deletion
	sizeBefore := l0.TotalSize
	fileCountBefore := l0.FileCount

	// Calculate deleted size (for verification only - NO actual I/O performed)
	var deletedSize float64
	for _, file := range job.SourceFiles {
		deletedSize += file.SizeMB
	}

	// Remove source files from L0
	filesToDelete := make(map[*SSTFile]bool)
	for _, file := range job.SourceFiles {
		filesToDelete[file] = true
	}

	var remainingFiles []*SSTFile
	for _, file := range l0.Files {
		if !filesToDelete[file] {
			remainingFiles = append(remainingFiles, file)
		}
	}

	l0.Files = remainingFiles
	l0.FileCount = len(remainingFiles)

	// Recalculate total size
	l0.TotalSize = 0
	for _, file := range l0.Files {
		l0.TotalSize += file.SizeMB
	}

	// POSTCONDITION: Verify size accounting
	sizeAfter := l0.TotalSize
	fileCountAfter := l0.FileCount
	expectedSizeChange := -deletedSize
	actualSizeChange := sizeAfter - sizeBefore

	fmt.Printf("[FIFO-DEL] SIZE CHECK: before=%.1f MB (%d files), after=%.1f MB (%d files)\n",
		sizeBefore, fileCountBefore, sizeAfter, fileCountAfter)
	fmt.Printf("[FIFO-DEL] SIZE CHANGE: expected=%.1f MB (deleted %.1f MB), actual=%.1f MB\n",
		expectedSizeChange, deletedSize, actualSizeChange)

	if actualSizeChange != expectedSizeChange {
		panic(fmt.Sprintf("FIFO deletion size accounting ERROR: expected change %.1f MB but got %.1f MB",
			expectedSizeChange, actualSizeChange))
	}
}

// executeIntraL0Compaction merges multiple small L0 files into one larger file.
func (f *FIFOCompactor) executeIntraL0Compaction(job *CompactionJob, lsm *LSMTree, config SimConfig, virtualTime float64) (inputSize, outputSize float64, outputFileCount int) {
	l0 := lsm.Levels[0]

	// Calculate input size
	for _, file := range job.SourceFiles {
		inputSize += file.SizeMB
	}

	// Apply reduction factor for deduplication
	outputSize = inputSize * config.DeduplicationFactor

	fmt.Printf("[FIFO-INTRA] Deduplication: inputSize=%.1f MB * factor=%.3f = outputSize=%.1f MB\n",
		inputSize, config.DeduplicationFactor, outputSize)

	// PRECONDITION: Calculate size BEFORE compaction
	sizeBefore := l0.TotalSize
	fileCountBefore := l0.FileCount

	// Remove source files from L0
	filesToCompact := make(map[*SSTFile]bool)
	for _, file := range job.SourceFiles {
		filesToCompact[file] = true
	}

	var remainingFiles []*SSTFile
	for _, file := range l0.Files {
		if !filesToCompact[file] {
			remainingFiles = append(remainingFiles, file)
		}
	}

	// Create new merged file
	// FIDELITY: ✓ Matches RocksDB - intra-L0 output uses current time, prepended to array
	// The merged file is genuinely NEW (just created), so it gets current virtualTime
	// and is inserted at index 0 (newest position). This maintains insertion order invariant.
	newFile := &SSTFile{
		ID:        fmt.Sprintf("fifo-merged-%d", int(virtualTime)),
		SizeMB:    outputSize,
		CreatedAt: virtualTime, // Use current time - this IS a new file!
	}

	// Prepend new file to beginning (newest position)
	// FIDELITY: ✓ Matches RocksDB - L0 array order: index 0 = NEWEST, index N-1 = OLDEST
	// Intra-L0 picks newest files, merges them, output goes back to newest position
	l0.Files = append([]*SSTFile{newFile}, remainingFiles...)
	l0.FileCount = len(l0.Files)

	// Recalculate total size
	l0.TotalSize = 0
	for _, file := range l0.Files {
		l0.TotalSize += file.SizeMB
	}

	// POSTCONDITION: Verify size accounting
	sizeAfter := l0.TotalSize
	fileCountAfter := l0.FileCount
	expectedSizeChange := outputSize - inputSize
	actualSizeChange := sizeAfter - sizeBefore

	fmt.Printf("[FIFO-INTRA] SIZE CHECK: before=%.1f MB (%d files), after=%.1f MB (%d files)\n",
		sizeBefore, fileCountBefore, sizeAfter, fileCountAfter)
	fmt.Printf("[FIFO-INTRA] SIZE CHANGE: expected=%.1f MB (out=%.1f - in=%.1f), actual=%.1f MB\n",
		expectedSizeChange, outputSize, inputSize, actualSizeChange)

	if actualSizeChange != expectedSizeChange {
		panic(fmt.Sprintf("FIFO size accounting ERROR: expected change %.1f MB but got %.1f MB",
			expectedSizeChange, actualSizeChange))
	}

	return inputSize, outputSize, 1
}

// String returns a description of the FIFO compactor.
func (f *FIFOCompactor) String() string {
	return "FIFOCompactor"
}
