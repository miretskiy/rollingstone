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
	TargetFileSizeMB          int     `json:"targetFileSizeMB"`          // target_file_size_base (default 64MB)
	TargetFileSizeMultiplier  int     `json:"targetFileSizeMultiplier"`  // target_file_size_multiplier (default 1, but 2 makes sense for deeper levels)
	CompactionReductionFactor float64 `json:"compactionReductionFactor"` // Custom: deduplication/compression (0.5-1.0)

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
		CompactionReductionFactor:        0.9,                      // 10% reduction (dedup/compression)
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
		CompactionReductionFactor:        0.9,                      // 10% reduction
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
