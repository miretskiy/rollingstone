<!-- edbfe324-2370-4af4-9f80-943474be5c95 90a1f1cb-a737-44ae-bd52-3dd7fd2b69ea -->
# Implement Faithful RocksDB Compaction Simulation

## Core Issues

Current implementation (`simulator/lsm.go:188-229`) compacts ALL files from a level, doesn't model read I/O, and lacks compaction scoring. Per [RocksDB Leveled Compaction](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction), we need overlap-based file selection and proper I/O accounting.

## Implementation Plan

### 1. Add Distribution-Based File Selection

**File: `simulator/compactor.go` (NEW)**

Add helper functions for probabilistic file selection:

```go
// pickFileCount selects number of files to compact using distribution
func pickFileCount(availableFiles int, minFiles int, distribution string) int

// pickOverlapCount estimates overlapping files in target level using exponential distribution
// High probability of 1-2 files, occasional massive overlaps
func pickOverlapCount(maxFiles int, distribution string) int
```

**Distributions to support:**

- `uniform`: Equal probability (1/N for each count)
- `exponential`: Heavily skewed toward small numbers (most common for overlaps)
- `geometric`: P(k) = (1-p)^(k-1) * p (good for file selection)

### 2. No Key Range Tracking Needed

SST files remain simple - no MinKey/MaxKey fields. We simulate overlaps statistically instead of tracking exact ranges.

### 3. Create Compactor Interface and Implementation

**File: `simulator/compactor.go` (NEW)**

Define interface:

```go
type Compactor interface {
    // NeedsCompaction checks if a level needs compaction
    NeedsCompaction(level int, lsm *LSMTree, config SimConfig) bool
    
    // PickCompaction selects files to compact and returns compaction job
    PickCompaction(level int, lsm *LSMTree, config SimConfig) *CompactionJob
    
    // ExecuteCompaction performs the compaction
    ExecuteCompaction(job *CompactionJob, lsm *LSMTree, virtualTime float64) (inputSize, outputSize float64)
}

type CompactionJob struct {
    FromLevel      int
    ToLevel        int
    SourceFiles    []*SSTFile  // Files to compact from source level
    TargetFiles    []*SSTFile  // Overlapping files in target level
    IsIntraL0      bool        // True if this is intra-L0 compaction
}
```

Implement `LeveledCompactor` with realistic file selection:

**For Intra-L0 compaction** (reduce overlapping files within L0):

- Triggered when L0 file count exceeds threshold but not yet ready for L0→L1
- Pick subset of overlapping L0 files (based on `level0_file_num_compaction_trigger`)
- Merge them into fewer L0 files (still in L0, not promoted to L1)
- Reduces read amplification before L0→L1 compaction

**For L0→L1:**

- Pick all L0 files (they overlap due to being unsorted)
- Find all L1 files that overlap the L0 key range
- Input = all L0 files + overlapping L1 files
- Output = merged, deduplicated files at L1

**For Ln→Ln+1 (n ≥ 1):**

- Pick 1 file from Ln (round-robin or oldest first)
- Find overlapping files in Ln+1
- Expand Ln selection if new Ln files overlap the Ln+1 range
- Input = selected Ln files + overlapping Ln+1 files
- Output = merged files at Ln+1

### 4. Model Read I/O in Addition to Write I/O

**File: `simulator/simulator.go:226-241` (processFlush and processCompaction)**

Currently:

```go
ioTimeSec := (inputSize + outputSize) / s.config.IOThroughputMBps
```

This already accounts for reading input + writing output, which is correct. Verify this is consistently applied across all compaction paths.

### 5. Implement Compaction Scoring

**File: `simulator/lsm.go`**

Add method `calculateCompactionScore(level int, config SimConfig) float64`:

Per [RocksDB docs](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction#compaction-picking):

- **L0 score** = max(fileCount / level0_file_num_compaction_trigger, totalSize / max_bytes_for_level_base)
- **Ln score** = totalSize / targetSize (excluding files already being compacted)

Add method `pickLevelToCompact() int` - returns level with highest score > 1.0

### 6. Support Both Level Sizing Modes

**File: `simulator/config.go`**

Add field:

```go
type SimConfig struct {
    // ... existing fields
    LevelCompactionDynamicLevelBytes bool `json:"levelCompactionDynamicLevelBytes"` // Default: false (more intuitive)
}
```

**File: `simulator/lsm.go`**

Add method `calculateLevelTargets(config SimConfig) []float64`:

- **If false (default)**: Simple exponential sizing
  - Target_Size(L1) = max_bytes_for_level_base
  - Target_Size(Ln+1) = Target_Size(Ln) × max_bytes_for_level_multiplier
  - Easy to understand: L1=256MB, L2=2.56GB, L3=25.6GB, etc.

- **If true**: Dynamic sizing (RocksDB 8.4+ default)
  - Work backwards from largest level
  - Target_Size(Ln-1) = Target_Size(Ln) / multiplier
  - Guarantees 90% of data in last level (better space amp)
  - Skip levels with target < max_bytes_for_level_base / multiplier

### 7. Track Parallel Compactions

**File: `simulator/simulator.go`**

Add field to `Simulator`:

```go
type Simulator struct {
    // ... existing
    activeCompactions map[int]bool // Track which levels are compacting
}
```

In `processCompaction`, check:

- If `len(activeCompactions) >= MaxBackgroundJobs`, defer compaction
- L0→L1 compaction blocks other L0→L1 (not parallelized by default)
- Ln→Ln+1 compactions can run in parallel if they don't overlap

### 8. Update Compaction Triggering Logic

**File: `simulator/simulator.go:223-241`**

Replace level-by-level triggering with:

1. Calculate scores for all levels
2. Pick level with highest score
3. Check if compaction can start (parallel limit, disk availability)
4. Execute compaction using overlap-based file selection

### 9. Update UI Configuration

**File: `web/src/types.ts`**

Add `levelCompactionDynamicLevelBytes: boolean` to `SimulationConfig`

**File: `web/src/components/SimulationControls.tsx`**

Add toggle in "Advanced LSM Tuning" section for dynamic level bytes mode

### 10. Testing & Validation

- Verify L0 files are ~64MB (not 20GB)
- Verify not all files are compacted at once
- Verify read+write I/O is accounted
- Verify compaction scoring picks correct level
- Run with 250 MB/s writes, 500 MB/s I/O, observe gradual compactions

## Key Files Modified

- `simulator/lsm.go` - Core compaction logic (~200 lines changed)
- `simulator/simulator.go` - Scoring and parallel compaction (~50 lines)
- `simulator/config.go` - Add dynamic_level_bytes flag
- `web/src/types.ts` - UI config types
- `web/src/components/SimulationControls.tsx` - UI toggle

## Expected Behavior After Implementation

- L0 files remain ~64MB each
- Compactions select subset of files based on overlaps
- Multiple levels can compact in parallel (up to max_background_jobs)
- Read I/O contributes to disk saturation
- Write amplification reflects realistic partial compactions

### To-dos

- [ ] Phase 1: Setup Next.js + TypeScript project with Tailwind
- [ ] Phase 1: Build core simulation engine (event loop, memtable, flush logic)
- [ ] Phase 1: Create UI with playback controls and L0 visualization
- [ ] Phase 2: Implement L0→L1 compaction with reduction factor
- [ ] Phase 2: Add 2-level visualization and write amplification metrics
- [ ] Phase 3: Implement full multi-level LSM tree and compaction
- [ ] Phase 4: Add read simulation and read amplification tracking
- [ ] Phase 5: Implement IO profiles and performance modeling
- [ ] Phase 6: Add advanced configurations and UI polish