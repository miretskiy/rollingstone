package integration

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miretskiy/rollingstone/simulator"
)

// RocksDBConfig defines configuration for the RocksDB component model
type RocksDBConfig struct {
	// LSM Tree Configuration
	NumLevels              int `yaml:"num_levels" json:"num_levels"`
	MemtableFlushSizeMB    int `yaml:"memtable_flush_size_mb" json:"memtable_flush_size_mb"`
	L0CompactionTrigger    int `yaml:"l0_compaction_trigger" json:"l0_compaction_trigger"`
	MaxBytesForLevelBaseMB int `yaml:"max_bytes_for_level_base_mb" json:"max_bytes_for_level_base_mb"`
	LevelMultiplier        int `yaml:"level_multiplier" json:"level_multiplier"`
	TargetFileSizeMB       int `yaml:"target_file_size_mb" json:"target_file_size_mb"`

	// Compaction Configuration
	MaxBackgroundJobs int `yaml:"max_background_jobs" json:"max_background_jobs"`

	// I/O Configuration
	IOThroughputMBps float64 `yaml:"io_throughput_mbps" json:"io_throughput_mbps"`
	IOLatencyMs      float64 `yaml:"io_latency_ms" json:"io_latency_ms"`

	// Write Configuration
	WriteBatchSizeMB       float64  `yaml:"write_batch_size_mb" json:"write_batch_size_mb"`
	WriteStallLogThreshold float64  `yaml:"write_stall_log_threshold_ms" json:"write_stall_log_threshold_ms"`
	ErrorRate              *float64 `yaml:"error_rate,omitempty" json:"error_rate,omitempty"`
	ErrorType              *string  `yaml:"error_type,omitempty" json:"error_type,omitempty"`
}

// GensimRequestContext contains information about the incoming request
type GensimRequestContext struct {
	Component   string
	CurrentTime float64
}

// GensimLogEntry represents a log emitted by the model
type GensimLogEntry struct {
	OffsetMs float64
	Status   string
	Message  string
}

// GensimMetricSample represents a custom metric emitted by the model
type GensimMetricSample struct {
	Name  string
	Type  string
	Value float64
	Tags  map[string]string
}

// GensimParameterDescriptor describes a mutable configuration field
type GensimParameterDescriptor struct {
	Name         string      `json:"name"`
	Type         string      `json:"type"`
	CurrentValue interface{} `json:"current_value"`
	Min          *float64    `json:"min,omitempty"`
	Max          *float64    `json:"max,omitempty"`
	Description  string      `json:"description,omitempty"` // Detailed explanation of what this parameter does
}

// GensimResult represents the outcome of the model simulation for a request
type GensimResult struct {
	DurationMs float64
	WaitTimeMs float64
	Status     string
	ErrorType  *string
	ErrorMsg   *string
	Logs       []GensimLogEntry
	Metrics    []GensimMetricSample
}

// RocksDBModel implements a RocksDB LSM tree component using rollingstone simulator
type RocksDBModel struct {
	component string
	cfg       *RocksDBConfig
	mu        sync.Mutex
	sim       *simulator.Simulator
	rng       *rand.Rand

	// Tracking for metrics
	totalWrites                 int64
	totalWriteBytes             float64
	totalWriteTimeMs            float64
	totalCompactionBytesRead    float64 // Cumulative compaction input bytes
	totalCompactionBytesWritten float64 // Cumulative compaction output bytes
	totalWriteStalls            int64   // Cumulative write stall count
	lastStallState              bool    // Track previous stall state to detect transitions

	lastHealth         string  // Last reported generic health ("ok", "warn", "error")
	lastHealthStatus   string  // Last reported detailed health ("normal", "stalled", "oom_killed")
	healthStatusExpiry float64 // Virtual time when the last health status expires (decays back to normal)
}

// NewRocksDBModel creates a new RocksDB component model
func NewRocksDBModel(component string, cfg *RocksDBConfig) (*RocksDBModel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("rocksdb config is required")
	}

	// Validate configuration
	if cfg.NumLevels < 3 || cfg.NumLevels > 9 {
		return nil, fmt.Errorf("num_levels must be between 3 and 9, got %d", cfg.NumLevels)
	}
	if cfg.MemtableFlushSizeMB <= 0 {
		return nil, fmt.Errorf("memtable_flush_size_mb must be positive, got %d", cfg.MemtableFlushSizeMB)
	}
	if cfg.MaxBackgroundJobs <= 0 {
		return nil, fmt.Errorf("max_background_jobs must be positive, got %d", cfg.MaxBackgroundJobs)
	}
	if cfg.IOThroughputMBps <= 0 {
		return nil, fmt.Errorf("io_throughput_mbps must be positive, got %f", cfg.IOThroughputMBps)
	}
	if cfg.WriteBatchSizeMB <= 0 {
		cfg.WriteBatchSizeMB = 1.0 // Default to 1MB batches
	}

	// Create simulator configuration
	simCfg := simulator.SimConfig{
		NumLevels:                        cfg.NumLevels,
		MemtableFlushSizeMB:              cfg.MemtableFlushSizeMB,
		L0CompactionTrigger:              cfg.L0CompactionTrigger,
		MaxBytesForLevelBaseMB:           cfg.MaxBytesForLevelBaseMB,
		LevelMultiplier:                  cfg.LevelMultiplier,
		TargetFileSizeMB:                 cfg.TargetFileSizeMB,
		MaxBackgroundJobs:                cfg.MaxBackgroundJobs,
		IOThroughputMBps:                 cfg.IOThroughputMBps,
		IOLatencyMs:                      cfg.IOLatencyMs,
		WriteRateMBps:                    0, // We'll control writes through HandleRequest
		MaxWriteBufferNumber:             2,
		TargetFileSizeMultiplier:         1,
		CompactionReductionFactor:        0.9,
		MaxSubcompactions:                1,
		MaxCompactionBytesMB:             0,
		LevelCompactionDynamicLevelBytes: false,
		CompactionStyle:                  simulator.CompactionStyleLeveled,
		MaxSizeAmplificationPercent:      200,
		InitialLSMSizeMB:                 0,
		SimulationSpeedMultiplier:        1.0,
		RandomSeed:                       time.Now().UnixNano(),
		OverlapDistribution: simulator.OverlapDistributionConfig{
			Type:              simulator.DistExponential,
			ExponentialLambda: 0.5,
		},
		TrafficDistribution: simulator.TrafficDistributionConfig{
			Model:         simulator.TrafficModelConstant,
			WriteRateMBps: 0,
		},
	}

	// Create simulator instance
	sim, err := simulator.NewSimulator(simCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create simulator: %w", err)
	}

	// Initialize simulator
	if err := sim.Reset(); err != nil {
		return nil, fmt.Errorf("failed to reset simulator: %w", err)
	}

	model := &RocksDBModel{
		component:          component,
		cfg:                cfg,
		sim:                sim,
		rng:                rand.New(rand.NewSource(time.Now().UnixNano())),
		lastHealth:         "ok",
		lastHealthStatus:   "normal",
		healthStatusExpiry: 0,
	}

	return model, nil
}

// Name returns the component name
func (r *RocksDBModel) Name() string {
	return r.component
}

// evaluateImmediateHealthLocked inspects current simulator state to determine instantaneous health.
func (r *RocksDBModel) evaluateImmediateHealthLocked() (string, string) {
	if r.sim.IsWriteStalled() {
		state := r.sim.State()
		if memtableSizeMB, ok := state["memtableCurrentSizeMB"].(float64); ok {
			if memtableSizeMB > float64(r.cfg.MemtableFlushSizeMB)*2.0 {
				return "error", "oom_killed"
			}
		}
		return "warn", "stalled"
	}
	return "ok", "normal"
}

func (r *RocksDBModel) currentHealthLocked() (string, string) {
	now := r.sim.VirtualTime()
	generic, detailed := r.evaluateImmediateHealthLocked()

	switch generic {
	case "error":
		r.lastHealth = generic
		r.lastHealthStatus = detailed
		r.healthStatusExpiry = math.MaxFloat64
		return r.lastHealth, r.lastHealthStatus
	case "warn":
		r.lastHealth = generic
		r.lastHealthStatus = detailed
		visibility := math.Max(0.5, float64(r.cfg.MemtableFlushSizeMB)/r.cfg.IOThroughputMBps)
		r.healthStatusExpiry = now + visibility
		return r.lastHealth, r.lastHealthStatus
	default:
		if now > r.healthStatusExpiry {
			r.lastHealth = "ok"
			r.lastHealthStatus = "normal"
			r.healthStatusExpiry = now
		}
		return r.lastHealth, r.lastHealthStatus
	}
}

// Health returns the generic health status of the RocksDB model
func (r *RocksDBModel) Health() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	generic, _ := r.currentHealthLocked()
	return generic
}

// HealthStatus returns the detailed health status of the RocksDB model
func (r *RocksDBModel) HealthStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, detailed := r.currentHealthLocked()
	return detailed
}

// HandleRequest simulates a write request to RocksDB
func (r *RocksDBModel) HandleRequest(ctx *GensimRequestContext) (*GensimResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check health status - fail writes if error state (OOM killed)
	genericHealth, _ := r.evaluateImmediateHealthLocked()
	if genericHealth == "error" {
		errType := "oom_killed"
		errMsg := fmt.Sprintf("%s is OOM killed - writes are failing", r.component)
		r.lastHealth = "error"
		r.lastHealthStatus = "oom_killed"
		r.healthStatusExpiry = math.MaxFloat64
		return &GensimResult{
			Status:     "error",
			ErrorType:  &errType,
			ErrorMsg:   &errMsg,
			DurationMs: 0,
			WaitTimeMs: 0,
		}, nil
	}

	// Simulate writing a batch of data
	writeSizeMB := r.cfg.WriteBatchSizeMB

	// Get current state before write
	beforeVirtualTime := r.sim.VirtualTime()
	beforeMetrics := r.sim.Metrics()
	wasStalledBefore := r.lastStallState
	beforeTotalWrites := beforeMetrics.TotalDataWrittenMB
	targetTotalWrites := beforeTotalWrites + writeSizeMB

	// Check if we're stalled before the write
	isStalledBefore := r.sim.IsWriteStalled()

	// Schedule a write event at the current virtual time
	// If stalled, the simulator will reschedule this to a future time when the stall clears
	r.sim.ScheduleWrite(writeSizeMB, beforeVirtualTime)

	// Step the simulator forward to process the write and any triggered compactions
	// We estimate how long the write will take based on the write batch size and I/O speed
	estimatedWriteTimeSeconds := writeSizeMB / r.cfg.IOThroughputMBps
	if estimatedWriteTimeSeconds < 0.001 {
		estimatedWriteTimeSeconds = 0.001 // Minimum 1ms
	}

	// Advance the simulator until the write is accepted into the memtable (i.e., user write recorded)
	const completionEpsilon = 1e-6
	ackVirtualTime := beforeVirtualTime
	stepDelta := math.Max(estimatedWriteTimeSeconds, 0.001)
	if isStalledBefore {
		// When stalled, the write will only complete once a flush finishes. Use a larger step window.
		memtableFlushSeconds := float64(r.cfg.MemtableFlushSizeMB)/r.cfg.IOThroughputMBps + estimatedWriteTimeSeconds
		if memtableFlushSeconds > stepDelta {
			stepDelta = memtableFlushSeconds
		}
	}
	maxStep := math.Max(stepDelta*64, 0.01)

	var afterMetrics *simulator.Metrics
	completed := false
	for i := 0; i < 100; i++ {
		r.sim.StepByDelta(stepDelta)
		ackVirtualTime = r.sim.VirtualTime()
		afterMetrics = r.sim.Metrics()

		if afterMetrics.TotalDataWrittenMB >= targetTotalWrites-completionEpsilon {
			completed = true
			break
		}

		// Increase the step window gradually so we eventually reach the scheduled completion time
		if stepDelta < maxStep {
			stepDelta = math.Min(stepDelta*2, maxStep)
		}
	}

	if !completed {
		// Fallback: advance one more large window and capture the latest state
		r.sim.StepByDelta(stepDelta)
		ackVirtualTime = r.sim.VirtualTime()
		afterMetrics = r.sim.Metrics()
	}

	if afterMetrics == nil {
		afterMetrics = beforeMetrics
	}

	isStalledAfter := r.sim.IsWriteStalled()
	afterVirtualTime := ackVirtualTime

	// Wait time is the virtual time delta until the write is accepted (captures stalls/backpressure)
	waitTimeMs := math.Max(0, (ackVirtualTime-beforeVirtualTime)*1000)

	// Base write processing time (WAL append + memtable insert) scaled by size and disk characteristics
	actualWriteTimeMs := (writeSizeMB / r.cfg.IOThroughputMBps) * 1000
	if actualWriteTimeMs < 0.1 {
		actualWriteTimeMs = 0.1
	}
	actualWriteTimeMs += r.cfg.IOLatencyMs

	// Add jitter/variability (CPU contention, fsync jitter, etc.)
	actualWriteTimeMs += r.sampleLatencyVariation()
	if actualWriteTimeMs < r.cfg.IOLatencyMs {
		actualWriteTimeMs = r.cfg.IOLatencyMs
	}

	durationMs := waitTimeMs + actualWriteTimeMs

	// Update health hysteresis so the UI can observe recent stalls
	if waitTimeMs > 0 {
		visibility := math.Max((waitTimeMs/1000.0)*2.0, float64(r.cfg.MemtableFlushSizeMB)/r.cfg.IOThroughputMBps)
		r.lastHealth = "warn"
		r.lastHealthStatus = "stalled"
		r.healthStatusExpiry = ackVirtualTime + visibility
	} else if !r.sim.IsWriteStalled() {
		r.lastHealth = "ok"
		r.lastHealthStatus = "normal"
		r.healthStatusExpiry = ackVirtualTime
	}

	// Track write stall transitions (count when entering stall state)
	if !wasStalledBefore && isStalledAfter {
		r.totalWriteStalls++
	}
	r.lastStallState = isStalledAfter

	// Update tracking
	r.totalWrites++
	r.totalWriteBytes += writeSizeMB
	r.totalWriteTimeMs += durationMs

	// Track compaction bytes from metrics delta
	// Estimate compaction bytes: compaction throughput * time delta
	timeDeltaSeconds := (afterVirtualTime - beforeVirtualTime)
	if timeDeltaSeconds > 0 {
		// Compaction bytes written = compaction throughput * time
		compactionBytesWrittenDelta := afterMetrics.CompactionThroughputMBps * timeDeltaSeconds
		// Compaction bytes read is typically larger (reads input + overlap files)
		// Estimate as 1.5x written (accounts for reading input files and overlap files)
		compactionBytesReadDelta := compactionBytesWrittenDelta * 1.5
		r.totalCompactionBytesRead += compactionBytesReadDelta
		r.totalCompactionBytesWritten += compactionBytesWrittenDelta
	}

	result := &GensimResult{
		DurationMs: durationMs,
		WaitTimeMs: waitTimeMs,
		Status:     "ok",
	}

	// Check for errors
	if r.cfg.ErrorRate != nil && *r.cfg.ErrorRate > 0 {
		if r.rng.Float64() < *r.cfg.ErrorRate {
			result.Status = "error"
			if r.cfg.ErrorType != nil {
				result.ErrorType = r.cfg.ErrorType
			} else {
				errType := "write_error"
				result.ErrorType = &errType
			}
			msg := fmt.Sprintf("%s write failed", r.component)
			result.ErrorMsg = &msg
		}
	}

	// Add logs
	if waitTimeMs > r.cfg.WriteStallLogThreshold && r.cfg.WriteStallLogThreshold > 0 {
		result.Logs = append(result.Logs, GensimLogEntry{
			OffsetMs: 0,
			Status:   "warn",
			Message:  fmt.Sprintf("%s write stalled %.0fms (memtable backpressure)", r.component, waitTimeMs),
		})
	}

	// Add debug log for high write amplification
	if afterMetrics.WriteAmplification > 20 {
		result.Logs = append(result.Logs, GensimLogEntry{
			OffsetMs: durationMs,
			Status:   "info",
			Message:  fmt.Sprintf("%s high write amplification: %.1fx", r.component, afterMetrics.WriteAmplification),
		})
	}

	// Add metrics
	result.Metrics = r.buildMetrics(afterMetrics, beforeMetrics, durationMs, waitTimeMs, writeSizeMB)

	return result, nil
}

// sampleLatencyVariation adds realistic latency variation
func (r *RocksDBModel) sampleLatencyVariation() float64 {
	// Add some jitter: base latency + random variation
	baseLatency := r.cfg.IOLatencyMs
	variation := r.rng.Float64() * baseLatency * 0.5 // Up to 50% variation
	return variation
}

// buildMetrics constructs metric samples from simulator state
func (r *RocksDBModel) buildMetrics(afterMetrics, beforeMetrics *simulator.Metrics, durationMs, waitMs, writeSizeMB float64) []GensimMetricSample {
	tags := map[string]string{
		"component_model": "rocksdb",
	}

	// Get LSM state to access size information
	state := r.sim.State()
	totalSizeMB := 0.0
	memtableSizeMB := 0.0
	if val, ok := state["totalSizeMB"].(float64); ok {
		totalSizeMB = val
	}
	if val, ok := state["memtableCurrentSizeMB"].(float64); ok {
		memtableSizeMB = val
	}

	// Get per-level file counts from state
	perLevelFileCounts := make(map[int]int)
	if levels, ok := state["levels"].([]interface{}); ok {
		for _, levelInterface := range levels {
			if levelMap, ok := levelInterface.(map[string]interface{}); ok {
				if levelNum, ok := levelMap["level"].(float64); ok {
					if fileCount, ok := levelMap["fileCount"].(float64); ok {
						perLevelFileCounts[int(levelNum)] = int(fileCount)
					}
				}
			}
		}
	}

	// Check for pending compactions (compactions that should happen but haven't started)
	activeCompactions := r.sim.ActiveCompactions()
	compactionPending := 0
	// Check if there are levels that need compaction but don't have active compactions
	// This is a simplified check - in reality, RocksDB tracks pending compactions more precisely
	// Pending compactions exist when:
	// 1. L0 file count exceeds trigger AND we're not at max background jobs
	// 2. System is stalled (indicates compaction backlog)
	if l0Count, ok := perLevelFileCounts[0]; ok {
		if l0Count >= r.cfg.L0CompactionTrigger && len(activeCompactions) < r.cfg.MaxBackgroundJobs {
			compactionPending = 1
		}
	}
	// Also mark as pending if system is stalled (indicates compaction can't keep up)
	if afterMetrics.IsStalled {
		compactionPending = 1
	}

	samples := []GensimMetricSample{
		// Write metrics
		{
			Name:  "rocksdb.write_duration_ms",
			Type:  "gauge",
			Value: durationMs,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.write_wait_ms",
			Type:  "gauge",
			Value: waitMs,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.write_batch_size_mb",
			Type:  "gauge",
			Value: writeSizeMB,
			Tags:  tags,
		},
		// Cumulative counters (RocksDB-style raw statistics)
		{
			Name:  "rocksdb.keys_written",
			Type:  "counter",
			Value: float64(r.totalWrites),
			Tags:  tags,
		},
		{
			Name:  "rocksdb.bytes_written",
			Type:  "counter",
			Value: r.totalWriteBytes,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.compaction_bytes_read",
			Type:  "counter",
			Value: r.totalCompactionBytesRead,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.compaction_bytes_written",
			Type:  "counter",
			Value: r.totalCompactionBytesWritten,
			Tags:  tags,
		},
		// Write stall metrics
		{
			Name:  "rocksdb.write_stalls",
			Type:  "counter",
			Value: float64(r.totalWriteStalls),
			Tags:  tags,
		},
		{
			Name:  "rocksdb.write_stall_duration_ms",
			Type:  "gauge",
			Value: afterMetrics.StallDurationSeconds * 1000, // Convert seconds to ms
			Tags:  tags,
		},
		// Amplification metrics
		{
			Name:  "rocksdb.write_amplification",
			Type:  "gauge",
			Value: afterMetrics.WriteAmplification,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.read_amplification",
			Type:  "gauge",
			Value: afterMetrics.ReadAmplification,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.space_amplification",
			Type:  "gauge",
			Value: afterMetrics.SpaceAmplification,
			Tags:  tags,
		},
		// Size metrics
		{
			Name:  "rocksdb.total_size_mb",
			Type:  "gauge",
			Value: totalSizeMB,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.memtable_size_mb",
			Type:  "gauge",
			Value: memtableSizeMB,
			Tags:  tags,
		},
		// Compaction metrics
		{
			Name:  "rocksdb.active_compactions",
			Type:  "gauge",
			Value: float64(len(activeCompactions)),
			Tags:  tags,
		},
		{
			Name:  "rocksdb.compaction_pending",
			Type:  "gauge",
			Value: float64(compactionPending),
			Tags:  tags,
		},
		// Throughput metrics
		{
			Name:  "rocksdb.total_write_throughput_mbps",
			Type:  "gauge",
			Value: afterMetrics.TotalWriteThroughputMBps,
			Tags:  tags,
		},
		{
			Name:  "rocksdb.compaction_throughput_mbps",
			Type:  "gauge",
			Value: afterMetrics.CompactionThroughputMBps,
			Tags:  tags,
		},
	}

	// Add per-level throughput metrics
	for level, throughput := range afterMetrics.PerLevelThroughputMBps {
		levelTags := make(map[string]string)
		for k, v := range tags {
			levelTags[k] = v
		}
		levelTags["level"] = fmt.Sprintf("L%d", level)
		samples = append(samples, GensimMetricSample{
			Name:  "rocksdb.level_throughput_mbps",
			Type:  "gauge",
			Value: throughput,
			Tags:  levelTags,
		})
	}

	// Add per-level file count metrics (rocksdb.num_files_at_level)
	for level, fileCount := range perLevelFileCounts {
		levelTags := make(map[string]string)
		for k, v := range tags {
			levelTags[k] = v
		}
		levelTags["level"] = fmt.Sprintf("L%d", level)
		samples = append(samples, GensimMetricSample{
			Name:  "rocksdb.num_files_at_level",
			Type:  "gauge",
			Value: float64(fileCount),
			Tags:  levelTags,
		})
	}

	return samples
}

// Config returns the current model configuration
func (r *RocksDBModel) Config() map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	config := map[string]interface{}{
		"num_levels":                  r.cfg.NumLevels,
		"memtable_flush_size_mb":      r.cfg.MemtableFlushSizeMB,
		"l0_compaction_trigger":       r.cfg.L0CompactionTrigger,
		"max_bytes_for_level_base_mb": r.cfg.MaxBytesForLevelBaseMB,
		"level_multiplier":            r.cfg.LevelMultiplier,
		"target_file_size_mb":         r.cfg.TargetFileSizeMB,
		"max_background_jobs":         r.cfg.MaxBackgroundJobs,
		"io_throughput_mbps":          r.cfg.IOThroughputMBps,
		"io_latency_ms":               r.cfg.IOLatencyMs,
		"write_batch_size_mb":         r.cfg.WriteBatchSizeMB,
	}
	if r.cfg.WriteStallLogThreshold > 0 {
		config["write_stall_log_threshold_ms"] = r.cfg.WriteStallLogThreshold
	}
	return config
}

// MutableParameters returns descriptors for runtime-adjustable parameters
func (r *RocksDBModel) MutableParameters() []GensimParameterDescriptor {
	r.mu.Lock()
	defer r.mu.Unlock()

	params := make([]GensimParameterDescriptor, 0)

	minBatch := 0.1
	maxBatch := 100.0
	params = append(params, GensimParameterDescriptor{
		Name:         "write_batch_size",
		Type:         "size",
		CurrentValue: r.cfg.WriteBatchSizeMB, // Value in MB
		Min:          &minBatch,
		Max:          &maxBatch,
		Description:  "Size of each write batch in megabytes. Larger batches improve write throughput by amortizing I/O overhead, but increase memory usage and latency. Smaller batches reduce memory usage but may decrease throughput.",
	})

	minJobs := 1.0
	maxJobs := 16.0
	params = append(params, GensimParameterDescriptor{
		Name:         "max_background_jobs",
		Type:         "int",
		CurrentValue: r.cfg.MaxBackgroundJobs,
		Min:          &minJobs,
		Max:          &maxJobs,
		Description:  "Maximum number of concurrent background compaction and flush jobs. Increasing this allows more parallel compaction work, which can improve write throughput and reduce write stalls, but consumes more CPU and I/O resources. Decreasing it reduces resource usage but may cause compaction lag and write stalls.",
	})

	minThroughput := 10.0
	maxThroughput := 10000.0
	params = append(params, GensimParameterDescriptor{
		Name:         "io_throughput_mbps",
		Type:         "float",
		CurrentValue: r.cfg.IOThroughputMBps,
		Min:          &minThroughput,
		Max:          &maxThroughput,
		Description:  "Maximum I/O throughput in megabytes per second for disk operations (reads and writes). This simulates the storage device's bandwidth limit. Lower values simulate slower storage (e.g., HDDs or throttled EBS volumes), while higher values simulate faster storage (e.g., NVMe SSDs or high-performance EBS volumes). This directly affects compaction speed and write performance.",
	})

	return params
}

// UpdateParameters applies runtime configuration changes
func (r *RocksDBModel) UpdateParameters(params map[string]interface{}) error {
	if len(params) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Update write batch size (supports size strings with units like "100kb", "1mb")
	if raw, ok := params["write_batch_size"]; ok {
		val, err := parseSizeParam(raw)
		if err != nil {
			return fmt.Errorf("write_batch_size: %w", err)
		}
		if val <= 0 {
			return fmt.Errorf("write_batch_size must be > 0")
		}
		r.cfg.WriteBatchSizeMB = val // Store in MB
	}

	// Update background jobs
	if raw, ok := params["max_background_jobs"]; ok {
		val, err := parseIntParam(raw)
		if err != nil {
			return fmt.Errorf("max_background_jobs: %w", err)
		}
		if val <= 0 {
			return fmt.Errorf("max_background_jobs must be > 0")
		}
		r.cfg.MaxBackgroundJobs = val
		// Update simulator config
		newConfig := r.sim.Config()
		newConfig.MaxBackgroundJobs = val
		if err := r.sim.UpdateConfig(newConfig); err != nil {
			return fmt.Errorf("failed to update simulator config: %w", err)
		}
	}

	// Update I/O throughput
	if raw, ok := params["io_throughput_mbps"]; ok {
		val, err := parseFloatParam(raw)
		if err != nil {
			return fmt.Errorf("io_throughput_mbps: %w", err)
		}
		if val <= 0 {
			return fmt.Errorf("io_throughput_mbps must be > 0")
		}
		r.cfg.IOThroughputMBps = val
		// Update simulator config
		newConfig := r.sim.Config()
		newConfig.IOThroughputMBps = val
		if err := r.sim.UpdateConfig(newConfig); err != nil {
			return fmt.Errorf("failed to update simulator config: %w", err)
		}
	}

	return nil
}

// Helper functions for parameter parsing
func parseIntParam(value interface{}) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case float32:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func parseFloatParam(value interface{}) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

// parseSizeParam parses a size value that can be a number (assumed MB) or a string with units (e.g., "100kb", "1mb", "2gb")
func parseSizeParam(value interface{}) (float64, error) {
	// Handle string values with units
	if str, ok := value.(string); ok {
		return parseSizeString(str)
	}
	// Handle numeric values (assumed to be in MB)
	return parseFloatParam(value)
}

// parseSizeString parses a size string with optional units (kb, mb, gb, tb, b)
func parseSizeString(value string) (float64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty value")
	}

	value = strings.ToLower(strings.TrimSpace(value))

	// Try parsing as plain number first (assume MB)
	if num, err := strconv.ParseFloat(value, 64); err == nil {
		return num, nil
	}

	// Parse with units
	var numericValue float64
	var unit string

	// Try to extract number and unit
	if strings.HasSuffix(value, "kb") {
		unit = "kb"
		numericValue, _ = strconv.ParseFloat(strings.TrimSuffix(value, "kb"), 64)
	} else if strings.HasSuffix(value, "mb") {
		unit = "mb"
		numericValue, _ = strconv.ParseFloat(strings.TrimSuffix(value, "mb"), 64)
	} else if strings.HasSuffix(value, "gb") {
		unit = "gb"
		numericValue, _ = strconv.ParseFloat(strings.TrimSuffix(value, "gb"), 64)
	} else if strings.HasSuffix(value, "tb") {
		unit = "tb"
		numericValue, _ = strconv.ParseFloat(strings.TrimSuffix(value, "tb"), 64)
	} else if strings.HasSuffix(value, "b") && !strings.HasSuffix(value, "kb") && !strings.HasSuffix(value, "mb") && !strings.HasSuffix(value, "gb") && !strings.HasSuffix(value, "tb") {
		unit = "b"
		numericValue, _ = strconv.ParseFloat(strings.TrimSuffix(value, "b"), 64)
	} else {
		return 0, fmt.Errorf("unable to parse size value: %s", value)
	}

	if math.IsNaN(numericValue) || math.IsInf(numericValue, 0) {
		return 0, fmt.Errorf("invalid numeric value")
	}

	// Convert to MB
	switch unit {
	case "kb":
		return numericValue / 1024.0, nil
	case "mb":
		return numericValue, nil
	case "gb":
		return numericValue * 1024.0, nil
	case "tb":
		return numericValue * 1024.0 * 1024.0, nil
	case "b":
		return numericValue / (1024.0 * 1024.0), nil
	default:
		return numericValue, nil // Assume MB if unit not recognized
	}
}
