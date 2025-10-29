package simulator

// SimConfig holds all simulation parameters based on RocksDB tuning guide
// References: https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide
type SimConfig struct {
	// Write Path
	WriteRateMBps        float64 `json:"writeRateMBps"`        // Average write throughput (MB/s)
	MemtableFlushSizeMB  int     `json:"memtableFlushSizeMB"`  // write_buffer_size (default 64MB)
	MaxWriteBufferNumber int     `json:"maxWriteBufferNumber"` // max_write_buffer_number (default 2)

	// Compaction Triggers
	L0CompactionTrigger    int `json:"l0CompactionTrigger"`    // level0_file_num_compaction_trigger (default 4)
	MaxBytesForLevelBaseMB int `json:"maxBytesForLevelBaseMB"` // L1 target size (default 256MB)
	LevelMultiplier        int `json:"levelMultiplier"`        // max_bytes_for_level_multiplier (default 10)

	// SST Files
	TargetFileSizeMB          int     `json:"targetFileSizeMB"`          // target_file_size_base (default 64MB)
	TargetFileSizeMultiplier  int     `json:"targetFileSizeMultiplier"`  // target_file_size_multiplier (default 1, but 2 makes sense for deeper levels)
	CompactionReductionFactor float64 `json:"compactionReductionFactor"` // Custom: deduplication/compression (0.5-1.0)

	// Compaction Parallelism & Performance
	MaxBackgroundJobs                int     `json:"maxBackgroundJobs"`                // max_background_jobs (default 2) - parallel compactions
	MaxSubcompactions                int     `json:"maxSubcompactions"`                // max_subcompactions (default 1) - intra-compaction parallelism
	MaxCompactionBytesMB             int     `json:"maxCompactionBytesMB"`             // max_compaction_bytes - max total input size for single compaction (0 = auto: 25x target_file_size_base, per db/column_family.cc)
	IOLatencyMs                      float64 `json:"ioLatencyMs"`                      // Disk IO latency in milliseconds (seek time)
	IOThroughputMBps                 float64 `json:"ioThroughputMBps"`                 // Sequential I/O throughput in MB/s (for compaction duration)
	NumLevels                        int     `json:"numLevels"`                        // LSM tree depth (default 7)
	LevelCompactionDynamicLevelBytes bool    `json:"levelCompactionDynamicLevelBytes"` // level_compaction_dynamic_level_bytes (default false for intuition, true in RocksDB 8.6+)

	// Simulation Control
	InitialLSMSizeMB          int `json:"initialLSMSizeMB"`          // Pre-populate LSM with this much data (0 = start empty, useful for skipping warmup)
	SimulationSpeedMultiplier int `json:"simulationSpeedMultiplier"` // Process N events per step (1 = real-time feel, 10 = 10x faster)
}

// DefaultConfig returns sensible defaults based on RocksDB documentation
func DefaultConfig() SimConfig {
	return SimConfig{
		WriteRateMBps:                    10.0,  // 10 MB/s write rate
		MemtableFlushSizeMB:              64,    // 64MB memtable (RocksDB default)
		MaxWriteBufferNumber:             2,     // 2 memtables max (RocksDB default)
		L0CompactionTrigger:              4,     // 4 L0 files trigger compaction (RocksDB default)
		MaxBytesForLevelBaseMB:           256,   // 256MB L1 target (RocksDB default)
		LevelMultiplier:                  10,    // 10x multiplier (RocksDB default)
		TargetFileSizeMB:                 64,    // 64MB SST files (RocksDB default)
		TargetFileSizeMultiplier:         2,     // 2x multiplier per level (L1=64MB, L2=128MB, L3=256MB, etc.)
		CompactionReductionFactor:        0.9,   // 10% reduction (dedup/compression)
		MaxBackgroundJobs:                2,     // 2 parallel compactions (RocksDB default)
		MaxSubcompactions:                1,     // No intra-compaction parallelism (RocksDB default)
		MaxCompactionBytesMB:             1600,  // 25x target_file_size_base (RocksDB typical default)
		IOLatencyMs:                      5.0,   // 5ms seek time (typical SSD)
		IOThroughputMBps:                 500.0, // 500 MB/s throughput (typical SSD)
		NumLevels:                        7,     // 7 levels (RocksDB default)
		LevelCompactionDynamicLevelBytes: false, // false for more intuitive level sizing
		InitialLSMSizeMB:                 0,     // 0 = start empty
		SimulationSpeedMultiplier:        1,     // 1 = process 1 event per step (real-time feel)
	}
}

// ThreeLevelConfig returns a simplified 3-level configuration for testing
// Useful for understanding basic LSM behavior: Memtable → L0 → L1
func ThreeLevelConfig() SimConfig {
	return SimConfig{
		WriteRateMBps:                    10.0,  // 10 MB/s write rate
		MemtableFlushSizeMB:              64,    // 64MB memtable
		MaxWriteBufferNumber:             2,     // 2 memtables max
		L0CompactionTrigger:              4,     // 4 L0 files trigger compaction
		MaxBytesForLevelBaseMB:           256,   // 256MB L1 target
		LevelMultiplier:                  10,    // 10x multiplier (but only 3 levels total)
		TargetFileSizeMB:                 64,    // 64MB SST files
		TargetFileSizeMultiplier:         2,     // 2x multiplier per level
		CompactionReductionFactor:        0.9,   // 10% reduction
		MaxBackgroundJobs:                2,     // 2 parallel compactions
		MaxSubcompactions:                1,     // No intra-compaction parallelism
		IOLatencyMs:                      5.0,   // 5ms seek time
		IOThroughputMBps:                 500.0, // 500 MB/s throughput
		NumLevels:                        3,     // Only 3 levels: Memtable, L0, L1
		LevelCompactionDynamicLevelBytes: false, // false for more intuitive level sizing
		InitialLSMSizeMB:                 0,     // 0 = start empty
		SimulationSpeedMultiplier:        1,     // 1 = process 1 event per step
	}
}

// Validate checks if configuration values are reasonable
func (c *SimConfig) Validate() error {
	if c.WriteRateMBps < 0 {
		return ErrInvalidConfig("writeRateMBps must be >= 0")
	}
	if c.MemtableFlushSizeMB <= 0 {
		return ErrInvalidConfig("memtableFlushSizeMB must be > 0")
	}
	if c.MaxWriteBufferNumber < 1 {
		return ErrInvalidConfig("maxWriteBufferNumber must be >= 1")
	}
	if c.L0CompactionTrigger < 2 {
		return ErrInvalidConfig("l0CompactionTrigger must be >= 2")
	}
	if c.CompactionReductionFactor < 0.1 || c.CompactionReductionFactor > 1.0 {
		return ErrInvalidConfig("compactionReductionFactor must be between 0.1 and 1.0")
	}
	if c.MaxBackgroundJobs < 1 {
		return ErrInvalidConfig("maxBackgroundJobs must be >= 1")
	}
	if c.MaxSubcompactions < 1 {
		return ErrInvalidConfig("maxSubcompactions must be >= 1")
	}
	if c.IOThroughputMBps <= 0 {
		return ErrInvalidConfig("ioThroughputMBps must be > 0")
	}
	if c.NumLevels < 2 || c.NumLevels > 10 {
		return ErrInvalidConfig("numLevels must be between 2 and 10")
	}
	return nil
}
