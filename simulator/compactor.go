package simulator

// pickFileCount selects number of files to compact using distribution
func pickFileCount(availableFiles int, minFiles int, dist filePicker) int {
	if availableFiles <= minFiles {
		return availableFiles
	}
	return dist.Pick(minFiles, availableFiles)
}

// pickOverlapCount estimates overlapping files in target level
// Uses distribution to model overlaps - Geometric provides better balance than Exponential
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

	// PickCompaction selects the best compaction from the LSM state and returns a compaction job
	// Returns nil if no compaction is needed (after fast checks)
	// Returns non-nil CompactionJob if compaction should be executed
	// Compactor internally tracks activeCompactions to avoid double-scheduling
	// Matches RocksDB's design: PickCompaction takes no level parameter and picks best compaction
	PickCompaction(lsm *LSMTree, config SimConfig) *CompactionJob

	// ExecuteCompaction performs the compaction and clears internal tracking
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
