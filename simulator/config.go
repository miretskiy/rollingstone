package simulator

import (
	"encoding/json"
	"fmt"
)

// CompactionStyle represents the compaction strategy
// RocksDB Reference: compaction_style in db/options.h
// GitHub: https://github.com/facebook/rocksdb/blob/main/include/rocksdb/options.h
type CompactionStyle int

const (
	CompactionStyleLeveled   CompactionStyle = iota // Leveled compaction (classic RocksDB style)
	CompactionStyleUniversal                        // Universal compaction (space-efficient, lower write amp)
)

// TrafficModel represents the traffic distribution model
type TrafficModel int

const (
	TrafficModelConstant      TrafficModel = iota // Constant rate model
	TrafficModelAdvancedONOFF                     // Advanced ON/OFF lognormal model with spikes
)

// String returns the string representation of TrafficModel
func (tm TrafficModel) String() string {
	switch tm {
	case TrafficModelConstant:
		return "constant"
	case TrafficModelAdvancedONOFF:
		return "advanced"
	default:
		return "constant"
	}
}

// ParseTrafficModel parses a string into TrafficModel
func ParseTrafficModel(s string) (TrafficModel, error) {
	switch s {
	case "constant":
		return TrafficModelConstant, nil
	case "advanced":
		return TrafficModelAdvancedONOFF, nil
	default:
		return TrafficModelConstant, fmt.Errorf("invalid traffic model: %s (must be 'constant' or 'advanced')", s)
	}
}

// MarshalJSON implements json.Marshaler for TrafficModel
func (tm TrafficModel) MarshalJSON() ([]byte, error) {
	return json.Marshal(tm.String())
}

// UnmarshalJSON implements json.Unmarshaler for TrafficModel
func (tm *TrafficModel) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseTrafficModel(s)
	if err != nil {
		return err
	}
	*tm = parsed
	return nil
}

// TrafficDistributionConfig holds traffic distribution parameters
type TrafficDistributionConfig struct {
	Model TrafficModel `json:"model"` // Traffic model type

	// Constant model parameters
	WriteRateMBps float64 `json:"writeRateMBps"` // For constant model: write rate in MB/s

	// Advanced ON/OFF model parameters
	BaseRateMBps        float64 `json:"baseRateMBps"`        // B: baseline rate in MB/s
	BurstMultiplier     float64 `json:"burstMultiplier"`     // M: multiplier for burst regime
	LognormalSigma      float64 `json:"lognormalSigma"`      // σ: lognormal variance parameter
	OnMeanSeconds       float64 `json:"onMeanSeconds"`       // Mean ON duration
	OffMeanSeconds      float64 `json:"offMeanSeconds"`      // Mean OFF duration
	ErlangK             int     `json:"erlangK"`             // Erlang shape parameter for ON periods
	SpikeRatePerSec     float64 `json:"spikeRatePerSec"`     // Poisson rate for spike arrival
	SpikeMeanDur        float64 `json:"spikeMeanDur"`        // Mean spike duration
	SpikeAmplitudeMean  float64 `json:"spikeAmplitudeMean"`  // Mean spike amplitude (log space)
	SpikeAmplitudeSigma float64 `json:"spikeAmplitudeSigma"` // Spike amplitude variance (log space)
	CapacityLimitMB     float64 `json:"capacityLimitMB"`     // Capacity limit (0 = unlimited)
	QueueMode           string  `json:"queueMode"`           // "drop" or "queue"
}

// OverlapDistributionConfig holds overlap distribution parameters
type OverlapDistributionConfig struct {
	Type              DistributionType `json:"type"`                      // Distribution type: Uniform, Exponential, Geometric, Fixed
	GeometricP        float64          `json:"geometricP"`                // For Geometric: success probability (default 0.3)
	ExponentialLambda float64          `json:"exponentialLambda"`         // For Exponential: rate parameter (default 0.5)
	FixedPercentage   *float64         `json:"fixedPercentage,omitempty"` // For Fixed: percentage of level below that overlaps (0.0 to 1.0, default 0.5). Use pointer to distinguish "not set" from "explicitly set to 0.0"
}

// LatencyDistributionType represents the type of latency distribution
type LatencyDistributionType string

const (
	LatencyDistFixed     LatencyDistributionType = "fixed"     // Fixed latency (deterministic)
	LatencyDistExp       LatencyDistributionType = "exponential" // Exponential distribution
	LatencyDistLognormal LatencyDistributionType = "lognormal" // Lognormal distribution
)

// LatencySpec specifies a latency distribution for read operations
type LatencySpec struct {
	Distribution LatencyDistributionType `json:"distribution"` // Distribution type: "fixed", "exponential", "lognormal"
	Mean         float64                 `json:"mean"`         // Mean latency in milliseconds
}

// ReadWorkloadConfig holds read path modeling parameters
// This uses a statistical approach: no discrete read events, just calculated metrics
type ReadWorkloadConfig struct {
	Enabled        bool    `json:"enabled"`        // Enable read path modeling
	RequestsPerSec float64 `json:"requestsPerSec"` // Base read requests per second (will fluctuate if RequestRateVariability > 0)

	// Traffic variability (simpler than write traffic model)
	RequestRateVariability float64 `json:"requestRateVariability"` // Coefficient of variation for request rate (0 = constant, 0.2 = 20% std dev, typical range 0-0.5)

	// Request type distribution (percentages, should sum to ~1.0)
	// Remaining percentage after these three = point lookups with cache miss
	CacheHitRate      float64 `json:"cacheHitRate"`      // Percentage hitting block cache (default: 0.90)
	BloomNegativeRate float64 `json:"bloomNegativeRate"` // Percentage that are bloom filter negatives (default: 0.02)
	ScanRate          float64 `json:"scanRate"`          // Percentage that are range scans (default: 0.05)

	// Latency specifications per request type
	CacheHitLatency      LatencySpec `json:"cacheHitLatency"`      // Latency for cache hits (default: fixed, 0.001 ms)
	BloomNegativeLatency LatencySpec `json:"bloomNegativeLatency"` // Latency for bloom filter negatives (default: fixed, 0.01 ms)
	PointLookupLatency   LatencySpec `json:"pointLookupLatency"`   // Base latency for point lookups (scaled by read amp) (default: exponential, 2.0 ms)
	ScanLatency          LatencySpec `json:"scanLatency"`          // Latency for range scans (default: lognormal, 10.0 ms)

	// Request characteristics
	AvgScanSizeKB float64 `json:"avgScanSizeKB"` // Average scan size in KB (default: 16 KB)
}

// String returns the string representation of CompactionStyle
func (cs CompactionStyle) String() string {
	switch cs {
	case CompactionStyleLeveled:
		return "leveled"
	case CompactionStyleUniversal:
		return "universal"
	default:
		return "unknown"
	}
}

// ParseCompactionStyle parses a string into CompactionStyle
func ParseCompactionStyle(s string) (CompactionStyle, error) {
	switch s {
	case "leveled":
		return CompactionStyleLeveled, nil
	case "universal":
		return CompactionStyleUniversal, nil
	default:
		return CompactionStyleUniversal, fmt.Errorf("invalid compaction style: %s (must be 'leveled' or 'universal')", s)
	}
}

// MarshalJSON implements json.Marshaler for CompactionStyle
func (cs CompactionStyle) MarshalJSON() ([]byte, error) {
	return json.Marshal(cs.String())
}

// UnmarshalJSON implements json.Unmarshaler for CompactionStyle
func (cs *CompactionStyle) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseCompactionStyle(s)
	if err != nil {
		return err
	}
	*cs = parsed
	return nil
}

// SimConfig holds all simulation parameters based on RocksDB tuning guide
// References: https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide
type SimConfig struct {
	// Write Path
	WriteRateMBps        float64 `json:"writeRateMBps"`        // Average write throughput (MB/s)
	MemtableFlushSizeMB  int     `json:"memtableFlushSizeMB"`  // write_buffer_size (default 64MB)
	MaxWriteBufferNumber int     `json:"maxWriteBufferNumber"` // max_write_buffer_number (default 2)

	// Compaction Triggers
	L0CompactionTrigger    int `json:"l0CompactionTrigger"`    // level0_file_num_compaction_trigger (default 4)
	MaxBytesForLevelBaseMB int `json:"maxBytesForLevelBaseMB"` // Base level target size (default 256MB). In static mode, this is L1. In dynamic mode, this is the base_level (first non-empty level).
	LevelMultiplier        int `json:"levelMultiplier"`        // max_bytes_for_level_multiplier (default 10)

	// SST Files
	TargetFileSizeMB         int     `json:"targetFileSizeMB"`         // target_file_size_base (default 64MB)
	TargetFileSizeMultiplier int     `json:"targetFileSizeMultiplier"` // target_file_size_multiplier (default 1, but 2 makes sense for deeper levels)
	DeduplicationFactor      float64 `json:"deduplicationFactor"`      // Logical size reduction from tombstones/overwrites (0.9 = 10% dedup, 1.0 = no dedup)
	CompressionFactor        float64 `json:"compressionFactor"`        // Physical size reduction from compression (0.85 = ~18% with 4KB blocks, 0.7 = ~30% with larger blocks, 1.0 = no compression)

	// Compression CPU Performance
	// RocksDB uses compression algorithms like LZ4, Snappy, or Zstd which consume CPU cycles
	// These parameters model the CPU cost of compression/decompression on the critical path
	// Based on real benchmarks: LZ4 ~750 MB/s compress, 3700 MB/s decompress (single-threaded)
	CompressionThroughputMBps   float64 `json:"compressionThroughputMBps"`   // CPU throughput for compression (MB/s), 0 = infinite (no CPU cost)
	DecompressionThroughputMBps float64 `json:"decompressionThroughputMBps"` // CPU throughput for decompression (MB/s), 0 = infinite (no CPU cost)
	BlockSizeKB                 int     `json:"blockSizeKB"`                 // SST block size in KB (RocksDB default: 4 KB) - affects compression efficiency and read amplification

	// Compaction Parallelism & Performance
	MaxBackgroundJobs                int             `json:"maxBackgroundJobs"`                // max_background_jobs (default 2) - parallel compactions
	MaxSubcompactions                int             `json:"maxSubcompactions"`                // max_subcompactions (default 1) - intra-compaction parallelism
	MaxCompactionBytesMB             int             `json:"maxCompactionBytesMB"`             // max_compaction_bytes - max total input size for single compaction (0 = auto: 25x target_file_size_base, per db/column_family.cc)
	IOLatencyMs                      float64         `json:"ioLatencyMs"`                      // Disk IO latency in milliseconds (seek time)
	IOThroughputMBps                 float64         `json:"ioThroughputMBps"`                 // Sequential I/O throughput in MB/s (for compaction duration)
	NumLevels                        int             `json:"numLevels"`                        // LSM tree depth (default 7)
	LevelCompactionDynamicLevelBytes bool            `json:"levelCompactionDynamicLevelBytes"` // level_compaction_dynamic_level_bytes (default true) - ONLY applies to leveled compaction, ignored for universal compaction. When true, dynamically adjusts level sizes based on actual data distribution.
	CompactionStyle                  CompactionStyle `json:"compactionStyle"`                  // compaction_style: "leveled" or "universal" (default "universal")

	// Universal Compaction Options
	MaxSizeAmplificationPercent int `json:"maxSizeAmplificationPercent"` // max_size_amplification_percent (default 200%, RocksDB allows 0 to UINT_MAX) - max allowed space amplification before compaction triggers. 0 = trigger on any amplification, very high values (e.g., 9000) allow extreme amplification before triggering

	// Simulation Control
	InitialLSMSizeMB          int   `json:"initialLSMSizeMB"`          // Pre-populate LSM with this much data (0 = start empty, useful for skipping warmup)
	SimulationSpeedMultiplier int   `json:"simulationSpeedMultiplier"` // Process N events per step (1 = real-time feel, 10 = 10x faster)
	RandomSeed                int64 `json:"randomSeed"`                // Random seed for reproducibility (0 = use time-based seed)
	MaxStalledWriteMemoryMB   int   `json:"maxStalledWriteMemoryMB"`   // OOM threshold: stop simulation if stalled write backlog exceeds this (default 4096 MB = 4GB)

	// WAL (Write-Ahead Log) Configuration
	// RocksDB Reference: https://github.com/facebook/rocksdb/wiki/Write-Ahead-Log
	EnableWAL        bool    `json:"enableWAL"`        // Enable Write-Ahead Log (default true, matches RocksDB)
	WALSync          bool    `json:"walSync"`          // Sync WAL after each write (default false, matches RocksDB WriteOptions::sync)
	WALSyncLatencyMs float64 `json:"walSyncLatencyMs"` // fsync() latency in milliseconds (default 1.5ms for NVMe/SSD)

	// Traffic Distribution
	TrafficDistribution TrafficDistributionConfig `json:"trafficDistribution"` // Traffic distribution configuration

	// Overlap Distribution
	OverlapDistribution OverlapDistributionConfig `json:"overlapDistribution"` // Overlap distribution configuration

	// Read Path Modeling
	ReadWorkload *ReadWorkloadConfig `json:"readWorkload,omitempty"` // Read workload configuration (nil = disabled)
}

// DefaultConfig returns sensible defaults based on RocksDB documentation
func DefaultConfig() SimConfig {
	return SimConfig{
		WriteRateMBps:                    10.0,                     // 10 MB/s write rate (deprecated, use TrafficDistribution)
		MemtableFlushSizeMB:              64,                       // 64MB memtable (RocksDB default)
		MaxWriteBufferNumber:             2,                        // 2 memtables max (RocksDB default)
		L0CompactionTrigger:              4,                        // 4 L0 files trigger compaction (RocksDB default)
		MaxBytesForLevelBaseMB:           256,                      // 256MB L1 target (RocksDB default)
		LevelMultiplier:                  10,                       // 10x multiplier (RocksDB default)
		TargetFileSizeMB:                 64,                       // 64MB SST files (RocksDB default)
		TargetFileSizeMultiplier:         2,                        // 2x multiplier per level (L1=64MB, L2=128MB, L3=256MB, etc.)
		DeduplicationFactor:              0.9,                      // 10% logical reduction (tombstones, overwrites)
		CompressionFactor:                0.85,                     // 15% physical reduction with 4KB blocks (LZ4/Snappy), more realistic than 0.7
		CompressionThroughputMBps:        750,                      // LZ4 compression speed (single-threaded, from benchmarks)
		DecompressionThroughputMBps:      3700,                     // LZ4 decompression speed (single-threaded, from benchmarks)
		BlockSizeKB:                      4,                        // 4 KB block size (RocksDB default, verified in source)
		MaxBackgroundJobs:                2,                        // 2 parallel compactions (RocksDB default)
		MaxSubcompactions:                1,                        // No intra-compaction parallelism (RocksDB default)
		MaxCompactionBytesMB:             1600,                     // 25x target_file_size_base (RocksDB typical default)
		IOLatencyMs:                      1.0,                      // 1ms latency (EBS gp3 baseline)
		IOThroughputMBps:                 125.0,                    // 125 MB/s throughput (EBS gp3 baseline)
		NumLevels:                        7,                        // 7 levels (RocksDB default)
		LevelCompactionDynamicLevelBytes: true,                     // true matches RocksDB default (v8.2+)
		CompactionStyle:                  CompactionStyleUniversal, // Universal compaction (default as per user request)
		MaxSizeAmplificationPercent:      200,                      // 200% max size amplification (RocksDB default)
		InitialLSMSizeMB:                 0,                        // 0 = start empty
		SimulationSpeedMultiplier:        1,                        // 1 = process 1 event per step (real-time feel)
		RandomSeed:                       0,                        // 0 = use time-based seed
		MaxStalledWriteMemoryMB:          4096,                     // 4GB OOM threshold (reasonable default for simulator)
		EnableWAL:                        true,                     // WAL enabled (RocksDB default)
		WALSync:                          false,                    // Sync after each write (RocksDB WriteOptions::sync default: false)
		WALSyncLatencyMs:                 1.5,                      // 1.5ms fsync latency (typical NVMe/SSD)
		TrafficDistribution: TrafficDistributionConfig{
			Model:         TrafficModelConstant,
			WriteRateMBps: 10.0,
		},
		OverlapDistribution: OverlapDistributionConfig{
			Type:              DistGeometric,
			GeometricP:        0.3,
			ExponentialLambda: 0.5,
		},
		ReadWorkload: nil, // Disabled by default (nil = read path modeling not enabled)
	}
}

// DefaultReadWorkload returns default read workload configuration
// Represents typical production workload with high cache hit rate
func DefaultReadWorkload() ReadWorkloadConfig {
	return ReadWorkloadConfig{
		Enabled:                false, // Explicitly disabled by default
		RequestsPerSec:         1000,  // 1000 reads/sec (moderate load)
		RequestRateVariability: 0.0,   // No variability by default (constant rate)
		CacheHitRate:           0.90,  // 90% cache hits (typical for production)
		BloomNegativeRate:      0.02,  // 2% bloom filter negatives
		ScanRate:               0.05,  // 5% range scans
		// Remaining 3% = point lookups with cache miss

		// Latency specifications
		CacheHitLatency: LatencySpec{
			Distribution: LatencyDistFixed,
			Mean:         0.001, // 1 microsecond (memory access)
		},
		BloomNegativeLatency: LatencySpec{
			Distribution: LatencyDistFixed,
			Mean:         0.01, // 10 microseconds (bloom filter checks)
		},
		PointLookupLatency: LatencySpec{
			Distribution: LatencyDistExp,
			Mean:         2.0, // 2ms mean (will be scaled by read amplification)
		},
		ScanLatency: LatencySpec{
			Distribution: LatencyDistLognormal,
			Mean:         10.0, // 10ms mean for scans
		},
		AvgScanSizeKB: 16.0, // 16 KB average scan size
	}
}

// ThreeLevelConfig returns a simplified 3-level configuration for testing
// Useful for understanding basic LSM behavior: Memtable → L0 → L1
func ThreeLevelConfig() SimConfig {
	return SimConfig{
		WriteRateMBps:                    10.0,                     // 10 MB/s write rate (deprecated, use TrafficDistribution)
		MemtableFlushSizeMB:              64,                       // 64MB memtable
		MaxWriteBufferNumber:             2,                        // 2 memtables max
		L0CompactionTrigger:              4,                        // 4 L0 files trigger compaction
		MaxBytesForLevelBaseMB:           256,                      // 256MB L1 target
		LevelMultiplier:                  10,                       // 10x multiplier (but only 3 levels total)
		TargetFileSizeMB:                 64,                       // 64MB SST files
		TargetFileSizeMultiplier:         2,                        // 2x multiplier per level
		DeduplicationFactor:              0.9,                      // 10% logical reduction
		CompressionFactor:                0.85,                     // 15% physical reduction with 4KB blocks (LZ4/Snappy)
		CompressionThroughputMBps:        750,                      // LZ4 compression speed
		DecompressionThroughputMBps:      3700,                     // LZ4 decompression speed
		BlockSizeKB:                      4,                        // 4 KB block size (RocksDB default)
		MaxBackgroundJobs:                2,                        // 2 parallel compactions
		MaxSubcompactions:                1,                        // No intra-compaction parallelism
		IOLatencyMs:                      5.0,                      // 5ms seek time
		IOThroughputMBps:                 500.0,                    // 500 MB/s throughput
		NumLevels:                        3,                        // Only 3 levels: Memtable, L0, L1
		LevelCompactionDynamicLevelBytes: true,                     // true matches RocksDB default (v8.2+)
		CompactionStyle:                  CompactionStyleUniversal, // Default to universal
		MaxSizeAmplificationPercent:      200,                      // 200% max size amplification (RocksDB default)
		InitialLSMSizeMB:                 0,                        // 0 = start empty
		SimulationSpeedMultiplier:        1,                        // 1 = process 1 event per step
		RandomSeed:                       0,                        // 0 = use time-based seed
		MaxStalledWriteMemoryMB:          4096,                     // 4GB OOM threshold (reasonable default for simulator)
		EnableWAL:                        true,                     // WAL enabled (RocksDB default)
		WALSync:                          false,                    // Sync after each write (RocksDB WriteOptions::sync default: false)
		WALSyncLatencyMs:                 1.5,                      // 1.5ms fsync latency (typical NVMe/SSD)
		TrafficDistribution: TrafficDistributionConfig{
			Model:         TrafficModelConstant,
			WriteRateMBps: 10.0,
		},
		OverlapDistribution: OverlapDistributionConfig{
			Type:              DistGeometric,
			GeometricP:        0.3,
			ExponentialLambda: 0.5,
		},
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
	if c.DeduplicationFactor < 0.1 || c.DeduplicationFactor > 1.0 {
		return ErrInvalidConfig("deduplicationFactor must be between 0.1 and 1.0")
	}
	if c.CompressionFactor < 0.1 || c.CompressionFactor > 1.0 {
		return ErrInvalidConfig("compressionFactor must be between 0.1 and 1.0")
	}
	if c.CompressionThroughputMBps < 0 {
		return ErrInvalidConfig("compressionThroughputMBps must be >= 0 (0 = infinite/no CPU cost)")
	}
	if c.DecompressionThroughputMBps < 0 {
		return ErrInvalidConfig("decompressionThroughputMBps must be >= 0 (0 = infinite/no CPU cost)")
	}
	if c.BlockSizeKB < 1 || c.BlockSizeKB > 1024 {
		return ErrInvalidConfig("blockSizeKB must be between 1 and 1024")
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

	// RocksDB allows max_size_amplification_percent to be any unsigned int (0 to UINT_MAX)
	// No validation constraints in RocksDB - allows any value including:
	// - 0: triggers compaction on any positive amplification (aggressive compaction)
	// - Very high values (e.g., 9000): allows extreme space amplification before triggering
	// We validate it's non-negative to match RocksDB's unsigned int constraint
	if c.MaxSizeAmplificationPercent < 0 {
		return ErrInvalidConfig("maxSizeAmplificationPercent must be >= 0")
	}
	// CompactionStyle validation: type-safe enum, no additional validation needed
	return nil
}

// ================================
// Compression Presets
// ================================
// Based on real-world benchmarks (single-threaded performance)
// Source: https://github.com/lz4/lz4, https://github.com/google/snappy, https://facebook.github.io/zstd/

// WithLZ4Compression configures the simulator to use LZ4 compression characteristics
// LZ4 is RocksDB's default and provides very fast decompression with decent compression ratio
// Typical use case: Balanced performance for most workloads
func (c *SimConfig) WithLZ4Compression() *SimConfig {
	c.CompressionFactor = 0.85               // ~15% size reduction with 4KB blocks
	c.CompressionThroughputMBps = 750        // LZ4 compression speed (MB/s, single-threaded)
	c.DecompressionThroughputMBps = 3700     // LZ4 decompression speed (MB/s, single-threaded)
	return c
}

// WithSnappyCompression configures the simulator to use Snappy compression characteristics
// Snappy is slightly slower than LZ4 but was RocksDB's original default
// Typical use case: Legacy compatibility or when moderate CPU usage is acceptable
func (c *SimConfig) WithSnappyCompression() *SimConfig {
	c.CompressionFactor = 0.83               // ~17% size reduction with 4KB blocks
	c.CompressionThroughputMBps = 530        // Snappy compression speed (MB/s, single-threaded)
	c.DecompressionThroughputMBps = 1800     // Snappy decompression speed (MB/s, single-threaded)
	return c
}

// WithZstdCompression configures the simulator to use Zstandard compression characteristics
// Zstd provides better compression ratio at the cost of slower compression speed
// Typical use case: Storage-optimized workloads where CPU is abundant and I/O is expensive
func (c *SimConfig) WithZstdCompression() *SimConfig {
	c.CompressionFactor = 0.70               // ~30% size reduction with 4KB blocks (level 3 default)
	c.CompressionThroughputMBps = 470        // Zstd compression speed (MB/s, single-threaded, level 3)
	c.DecompressionThroughputMBps = 1380     // Zstd decompression speed (MB/s, single-threaded)
	return c
}

// WithNoCompression configures the simulator to disable compression
// Useful for workloads where data is already compressed or incompressible
// Typical use case: Pre-compressed data (images, video) or maximum CPU conservation
func (c *SimConfig) WithNoCompression() *SimConfig {
	c.CompressionFactor = 1.0                // No compression (1:1 ratio)
	c.CompressionThroughputMBps = 0          // No CPU cost (infinite throughput)
	c.DecompressionThroughputMBps = 0        // No CPU cost (infinite throughput)
	return c
}
