package simulator

// WriteActivity tracks a write event for throughput calculation
type WriteActivity struct {
	StartTime float64 // Virtual time when write started
	EndTime   float64 // Virtual time when write completed
	SizeMB    float64 // Output size in MB
	InputMB   float64 // Input size in MB (for compactions)
	Level     int     // Source level (-1 = flush to L0, 0+ = compaction from level N)
	ToLevel   int     // Target level (for compactions)
}

// CompactionStats tracks aggregate compaction activity since last UI update
// Useful for high-speed simulations where individual compactions complete too quickly to see
type CompactionStats struct {
	Count            int     `json:"count"`            // Number of compactions completed
	TotalInputFiles  int     `json:"totalInputFiles"`  // Total source files compacted
	TotalOutputFiles int     `json:"totalOutputFiles"` // Total output files created
	TotalInputMB     float64 `json:"totalInputMB"`     // Total input data size
	TotalOutputMB    float64 `json:"totalOutputMB"`    // Total output data size
}

// Metrics tracks amplification factors and performance statistics
type Metrics struct {
	Timestamp float64 `json:"timestamp"` // Virtual time

	// Amplification factors
	WriteAmplification float64 `json:"writeAmplification"` // bytes written to disk / bytes written by flush (RocksDB-style)
	ReadAmplification  float64 `json:"readAmplification"`  // number of files checked during point lookup (RocksDB-style approximation)
	SpaceAmplification float64 `json:"spaceAmplification"` // disk space used / logical data size

	// Latencies
	WriteLatencyMs float64 `json:"writeLatencyMs"`
	ReadLatencyMs  float64 `json:"readLatencyMs"`

	// Cumulative counters
	TotalDataWrittenMB float64 `json:"totalDataWrittenMB"` // User writes
	TotalDataReadMB    float64 `json:"totalDataReadMB"`    // User reads (future)

	// Throughput tracking (MB/s) - smoothed via exponential moving average
	FlushThroughputMBps         float64         `json:"flushThroughputMBps"`         // Memtable flush rate (smoothed)
	CompactionThroughputMBps    float64         `json:"compactionThroughputMBps"`    // Total compaction write rate (smoothed)
	TotalWriteThroughputMBps    float64         `json:"totalWriteThroughputMBps"`    // Total disk write rate (smoothed)
	PerLevelThroughputMBps      map[int]float64 `json:"perLevelThroughputMBps"`      // Per-level compaction rates (smoothed)
	MaxSustainableWriteRateMBps float64         `json:"maxSustainableWriteRateMBps"` // Maximum sustainable write rate (conservative estimate based on average overhead)
	MinSustainableWriteRateMBps float64         `json:"minSustainableWriteRateMBps"` // Minimum sustainable write rate (worst-case based on buffer capacity)

	// In-progress activities (for UI display)
	InProgressCount   int                      `json:"inProgressCount"`   // Number of ongoing writes
	InProgressDetails []map[string]interface{} `json:"inProgressDetails"` // Details of ongoing writes

	// Aggregate stats since last UI update (for fast simulations)
	// Map of fromLevel -> stats for compactions that completed between UI updates
	CompactionsSinceUpdate map[int]CompactionStats `json:"compactionsSinceUpdate"` // Per-level aggregate compaction activity

	// Write stall metrics
	StalledWriteCount    int     `json:"stalledWriteCount"`    // Current number of WriteEvents queued during stall
	MaxStalledWriteCount int     `json:"maxStalledWriteCount"` // Peak stalled write count seen
	StallDurationSeconds float64 `json:"stallDurationSeconds"` // Cumulative time spent in stall state
	IsStalled            bool    `json:"isStalled"`            // Whether currently in write stall state
	IsOOMKilled          bool    `json:"isOOMKilled"`          // Whether simulation was killed due to OOM

	// Internal tracking
	totalDiskWrittenMB     float64         // Total bytes written to disk (including compaction)
	totalFlushWrittenMB    float64         // Total bytes written by flushes (RocksDB-style WA denominator)
	totalCompactionInputMB float64         // Total compaction input (read) size for overhead calculation
	logicalDataSizeMB      float64         // Estimated logical data size
	recentWrites           []WriteActivity // Recent write events for throughput calculation
	inProgressWrites       []WriteActivity // Currently executing writes (not yet completed)
	throughputWindow       float64         // Time window for throughput calculation (seconds)

	// Exponential moving average smoothing (alpha = 0.2 for ~5-sample average)
	smoothingAlpha float64 // 0.2 = smooth over ~5 samples
	isFirstSample  bool    // Track first sample to initialize EMA
}

// NewMetrics creates a new metrics tracker
func NewMetrics() *Metrics {
	return &Metrics{
		Timestamp:                   0,
		WriteAmplification:          1.0,
		ReadAmplification:           1.0,
		SpaceAmplification:          1.0,
		WriteLatencyMs:              0,
		ReadLatencyMs:               0,
		TotalDataWrittenMB:          0,
		TotalDataReadMB:             0,
		FlushThroughputMBps:         0,
		CompactionThroughputMBps:    0,
		TotalWriteThroughputMBps:    0,
		PerLevelThroughputMBps:      make(map[int]float64),
		MaxSustainableWriteRateMBps: 0,
		MinSustainableWriteRateMBps: 0,
		CompactionsSinceUpdate:      make(map[int]CompactionStats),
		totalDiskWrittenMB:          0,
		totalFlushWrittenMB:         0,
		totalCompactionInputMB:      0,
		logicalDataSizeMB:           0,
		recentWrites:                make([]WriteActivity, 0),
		inProgressWrites:            make([]WriteActivity, 0),
		throughputWindow:            5.0,  // 5-second sliding window
		smoothingAlpha:              0.2,  // Smooth over ~5 samples
		isFirstSample:               true, // Initialize EMA with first sample
		StalledWriteCount:           0,
		MaxStalledWriteCount:        0,
		StallDurationSeconds:        0,
		IsStalled:                   false,
		IsOOMKilled:                 false,
	}
}

// StartWrite begins tracking a write activity (call when write starts, not completes)
func (m *Metrics) StartWrite(inputMB, outputMB float64, startTime, endTime float64, fromLevel, toLevel int) {
	m.inProgressWrites = append(m.inProgressWrites, WriteActivity{
		StartTime: startTime,
		EndTime:   endTime,
		SizeMB:    outputMB,
		InputMB:   inputMB,
		Level:     fromLevel,
		ToLevel:   toLevel,
	})
}

// CompleteWrite moves a write from in-progress to completed
func (m *Metrics) CompleteWrite(endTime float64, level int) {
	// Find and remove the write from inProgressWrites
	for i, w := range m.inProgressWrites {
		if w.Level == level && w.EndTime == endTime {
			// Move to recentWrites
			m.recentWrites = append(m.recentWrites, w)
			// Remove from inProgressWrites
			m.inProgressWrites = append(m.inProgressWrites[:i], m.inProgressWrites[i+1:]...)
			break
		}
	}
}

// GetInProgressWrites returns a copy of currently executing writes
func (m *Metrics) GetInProgressWrites() []WriteActivity {
	return append([]WriteActivity{}, m.inProgressWrites...)
}

// RecordUserWrite records a write operation by the user
func (m *Metrics) RecordUserWrite(sizeMB float64) {
	m.TotalDataWrittenMB += sizeMB
	m.logicalDataSizeMB += sizeMB
}

// RecordFlush records a memtable flush (writes to disk)
func (m *Metrics) RecordFlush(sizeMB, startTime, endTime float64) {
	m.totalDiskWrittenMB += sizeMB
	m.totalFlushWrittenMB += sizeMB // Track flush bytes for RocksDB-style write amplification
	m.updateWriteAmplification()

	// Track flush write activity (level -1 = flush to L0)
	m.recentWrites = append(m.recentWrites, WriteActivity{
		StartTime: startTime,
		EndTime:   endTime,
		SizeMB:    sizeMB,
		Level:     -1,
	})
}

// RecordCompaction records a compaction (reads input, writes output)
// isTrivialMove: if true, this is a metadata-only operation (no disk writes, RocksDB optimization)
func (m *Metrics) RecordCompaction(inputSizeMB, outputSizeMB, startTime, endTime float64, fromLevel int, inputFileCount, outputFileCount int, isTrivialMove bool) {
	// Trivial moves are metadata-only operations (no disk writes) - RocksDB optimization
	// When files don't overlap with target level, RocksDB just updates file metadata (level pointer)
	// See: db/compaction/compaction_picker_level.cc (TryExtendNonL0TrivialMove)
	if isTrivialMove {
		// Don't count trivial moves as disk writes - they're metadata-only
		// Still track for aggregate stats (UI display) but don't contribute to write amplification
		stats := m.CompactionsSinceUpdate[fromLevel]
		stats.Count++
		stats.TotalInputFiles += inputFileCount
		stats.TotalOutputFiles += outputFileCount
		stats.TotalInputMB += inputSizeMB
		stats.TotalOutputMB += outputSizeMB
		m.CompactionsSinceUpdate[fromLevel] = stats
		return
	}

	// Compaction reads input files and writes output files
	m.totalDiskWrittenMB += outputSizeMB
	m.totalCompactionInputMB += inputSizeMB // Track input for overhead calculation

	// Note: We don't reduce logicalDataSizeMB here because it represents
	// the cumulative user writes. Compaction deduplicates/compresses data
	// on disk, but doesn't change how much data the user has written.
	// Space amplification = disk space / logical data will show overhead
	// from having multiple versions across levels.

	m.updateWriteAmplification()

	// Track compaction write activity
	m.recentWrites = append(m.recentWrites, WriteActivity{
		StartTime: startTime,
		EndTime:   endTime,
		SizeMB:    outputSizeMB,
		Level:     fromLevel,
	})

	// Aggregate stats for fast simulations (multiple compactions between UI updates)
	// Track per-level (fromLevel) for display in UI
	stats := m.CompactionsSinceUpdate[fromLevel]
	stats.Count++
	stats.TotalInputFiles += inputFileCount
	stats.TotalOutputFiles += outputFileCount
	stats.TotalInputMB += inputSizeMB
	stats.TotalOutputMB += outputSizeMB
	m.CompactionsSinceUpdate[fromLevel] = stats
}

// ResetAggregateStats resets the aggregate compaction stats after a UI update
// This allows tracking compactions that complete between UI updates (useful for fast simulations)
func (m *Metrics) ResetAggregateStats() {
	m.CompactionsSinceUpdate = make(map[int]CompactionStats)
}

// UpdateSpaceAmplification updates space amplification based on LSM tree state
//
// RocksDB Definition: Space Amplification = size_on_file_system / size_of_user_data
//
// RocksDB Approximation: "So the size of the last level will be a good estimation of user data size.
// So total size of the DB divided by the size of the last level will be a good estimation of space amplification."
//
// Reference: RocksDB blog post (2015-07-23): "Dynamic Level Size for Level-Based Compaction"
// https://github.com/facebook/rocksdb/blob/main/docs/_posts/2015-07-23-dynamic-level.markdown
//
// Why use last level size instead of cumulative user writes:
// - The last level contains the most recent version of each key
// - If we compact everything to the last level, that's the actual user data size
// - Naturally accounts for deletes/updates (unlike cumulative writes)
// - In steady state, updates add roughly the same as deletes remove
//
// Example: If total size is 1111MB (L0=1GB, L1=10GB, L2=100GB, L3=1000GB):
//   - Space amp = 1111GB / 1000GB = 1.111x (excellent space efficiency)
//
// FIDELITY: ✓ Matches RocksDB's approximation method
func (m *Metrics) UpdateSpaceAmplification(diskSpaceMB float64, lsmTree *LSMTree) {
	// Find the last non-empty level (deepest level with data)
	// This represents the "user data size" after all compactions
	lastLevelSizeMB := 0.0
	for i := len(lsmTree.Levels) - 1; i >= 0; i-- {
		if lsmTree.Levels[i].TotalSize > 0 {
			lastLevelSizeMB = lsmTree.Levels[i].TotalSize
			break
		}
	}

	if lastLevelSizeMB > 0 {
		m.SpaceAmplification = diskSpaceMB / lastLevelSizeMB
	} else {
		// No data on disk yet - space amplification is undefined (return 1.0 as default)
		m.SpaceAmplification = 1.0
	}
}

// updateWriteAmplification recalculates write amplification
//
// RocksDB Definition: Write Amplification = (bytes written by flushes + bytes written by compactions) / bytes written by flushes
//
// This separates compaction overhead from compression savings:
// - Compression happens during flush (user data → SST file format)
// - Compaction overhead is the extra I/O beyond the initial flush
//
// Reference: RocksDB BlobDB blog post (2021-05-26): "Write amp as the total amount of data written
// by flushes and compactions divided by the amount of data written by flushes"
//
// Example: If user writes 100MB, flush writes 80MB (compression), compaction writes 72MB:
//   - Our formula: 152MB / 80MB = 1.9x (isolates compaction overhead)
//   - User-centric formula: 152MB / 100MB = 1.52x (includes compression savings)
func (m *Metrics) updateWriteAmplification() {
	if m.totalFlushWrittenMB > 0 {
		m.WriteAmplification = m.totalDiskWrittenMB / m.totalFlushWrittenMB
	} else {
		m.WriteAmplification = 1.0
	}
}

// UpdateReadAmplification calculates read amplification based on LSM structure
//
// RocksDB Definition: Read amplification = number of files checked during a point lookup
//
// RocksDB Behavior (point lookup):
//   - Active memtable: Always checked (immutable memtables are already being flushed, not checked)
//   - All L0 files: Must check all (L0 is unsorted/tiered, files may overlap)
//   - One file per level L1+: Binary search finds the file containing the key
//
// Reference: RocksDB uses READ_AMP_TOTAL_READ_BYTES / READ_AMP_ESTIMATE_USEFUL_BYTES for byte-based
// read amplification, but file-count-based RA is a common approximation.
//
// We use file-count RA as a proxy for RocksDB's byte-count RA (simpler, correlates well).
//
// FIDELITY: ✓ Matches RocksDB's file-checking behavior for point lookups
func (m *Metrics) UpdateReadAmplification(lsmTree *LSMTree, numMemtables int) {
	// Read amplification = number of places to check for a key
	// - Active memtable only (1 if exists, 0 if empty) - immutable memtables are already flushing
	// - All L0 files (L0 is unsorted/tiered, must check all)
	// - 1 file per level in L1+ (sorted levels, binary search)

	// Count active memtable only (RocksDB doesn't check immutable memtables during reads)
	activeMemtableCount := 0
	if numMemtables > 0 {
		// If any memtable exists (active or immutable), active memtable exists
		activeMemtableCount = 1
	}

	l0FileCount := 0
	numLevels := len(lsmTree.Levels)
	if numLevels > 0 {
		l0FileCount = lsmTree.Levels[0].FileCount
	}

	m.ReadAmplification = float64(activeMemtableCount + l0FileCount + (numLevels - 1))

	// Floor of 1.0 (at least check memtable)
	if m.ReadAmplification < 1.0 {
		m.ReadAmplification = 1.0
	}
}

// calculateThroughput calculates INSTANTANEOUS write throughput
// Shows what's actively being written RIGHT NOW, not historical average
// FIX: Accounts for serialized compaction execution (diskBusyUntil serializes all disk operations)
func (m *Metrics) calculateThroughput() {
	// Use a narrow window around current time to capture "instantaneous" throughput
	// Window: [now - 0.05s, now + 0.05s] = 100ms total
	instantWindow := 0.05
	windowStart := m.Timestamp - instantWindow
	windowEnd := m.Timestamp + instantWindow

	// Clean up old completed writes (keep only recent history)
	validWrites := make([]WriteActivity, 0)
	for _, w := range m.recentWrites {
		if w.EndTime >= m.Timestamp-5.0 { // Keep 5s of history
			validWrites = append(validWrites, w)
		}
	}
	m.recentWrites = validWrites

	// Combine completed writes AND in-progress writes for instantaneous calculation
	allWrites := append([]WriteActivity{}, m.recentWrites...)
	allWrites = append(allWrites, m.inProgressWrites...)

	if len(allWrites) == 0 {
		m.FlushThroughputMBps = 0
		m.CompactionThroughputMBps = 0
		m.TotalWriteThroughputMBps = 0
		m.PerLevelThroughputMBps = make(map[int]float64)
		return
	}

	// Calculate instantaneous throughput
	// CRITICAL FIX: Compactions are serialized via diskBusyUntil, so we can only count
	// compactions that are ACTUALLY executing (not waiting). Find the active compaction.
	var flushBandwidth, compactionBandwidth float64
	perLevelBandwidth := make(map[int]float64)

	// Find the compaction that is currently using disk (only one can be active at a time)
	// Active compaction: startTime <= now <= endTime
	var activeCompaction *WriteActivity
	for i := range allWrites {
		w := &allWrites[i]
		if w.Level >= 0 { // Compaction (not flush)
			// Check if this compaction is active during the instantaneous window
			if w.StartTime <= m.Timestamp && m.Timestamp <= w.EndTime {
				// This compaction is active RIGHT NOW
				if activeCompaction == nil || w.StartTime > activeCompaction.StartTime {
					// Pick the most recently started active compaction
					activeCompaction = w
				}
			}
		}
	}

	// Process all writes, but only count active compactions
	for _, w := range allWrites {
		// Check if this write is active during the instantaneous window
		if w.EndTime < windowStart || w.StartTime > windowEnd {
			continue // Not active during this instant
		}

		// Calculate write bandwidth (MB/s)
		writeDuration := w.EndTime - w.StartTime
		if writeDuration <= 0 {
			continue
		}

		if w.Level == -1 {
			// Flush: only output bandwidth (writes to disk)
			bandwidth := w.SizeMB / writeDuration
			flushBandwidth += bandwidth
		} else {
			// Compaction: only count if it's the active compaction (serialized execution)
			// FIX: Compactions consume disk bandwidth for BOTH reading input AND writing output
			if activeCompaction != nil && w.StartTime == activeCompaction.StartTime && w.EndTime == activeCompaction.EndTime {
				// This is the active compaction - count total disk bandwidth (read + write)
				totalDiskBandwidth := (w.InputMB + w.SizeMB) / writeDuration
				compactionBandwidth += totalDiskBandwidth
				perLevelBandwidth[w.Level] += totalDiskBandwidth
			}
			// Waiting compactions are ignored (they're not using disk yet)
		}
	}

	// Apply exponential moving average (EMA) smoothing to reduce UI spikes
	// EMA formula: smoothed = alpha * instantaneous + (1-alpha) * previous_smoothed
	// alpha = 0.2 gives approximately 5-sample average

	totalBandwidth := flushBandwidth + compactionBandwidth

	if m.isFirstSample {
		// Initialize EMA with first sample
		m.FlushThroughputMBps = flushBandwidth
		m.CompactionThroughputMBps = compactionBandwidth
		m.TotalWriteThroughputMBps = totalBandwidth
		m.isFirstSample = false
	} else {
		// Apply EMA smoothing
		m.FlushThroughputMBps = m.smoothingAlpha*flushBandwidth + (1-m.smoothingAlpha)*m.FlushThroughputMBps
		m.CompactionThroughputMBps = m.smoothingAlpha*compactionBandwidth + (1-m.smoothingAlpha)*m.CompactionThroughputMBps
		m.TotalWriteThroughputMBps = m.smoothingAlpha*totalBandwidth + (1-m.smoothingAlpha)*m.TotalWriteThroughputMBps
	}

	// Set per-level throughput with EMA smoothing
	smoothedPerLevel := make(map[int]float64)
	for level, bandwidth := range perLevelBandwidth {
		if prevBandwidth, exists := m.PerLevelThroughputMBps[level]; exists {
			smoothedPerLevel[level] = m.smoothingAlpha*bandwidth + (1-m.smoothingAlpha)*prevBandwidth
		} else {
			smoothedPerLevel[level] = bandwidth // First sample for this level
		}
	}
	// Also decay levels that are no longer active
	for level, prevBandwidth := range m.PerLevelThroughputMBps {
		if _, active := perLevelBandwidth[level]; !active {
			// Decay towards zero
			smoothedPerLevel[level] = (1 - m.smoothingAlpha) * prevBandwidth
			if smoothedPerLevel[level] < 0.01 {
				smoothedPerLevel[level] = 0 // Threshold to avoid tiny values
			}
		}
	}
	m.PerLevelThroughputMBps = smoothedPerLevel
}

// calculateWorstCaseCompactionIO calculates the worst-case I/O per compaction
// for a given level, based on file sizes and compaction pattern.
//
// Worst-case pattern: Read 2 files from source level (not 1, because 1 = trivial move),
// 1 file from target level (overlap), write 1 file to target level.
// Total: 4 files per compaction.
//
// File sizes are calculated as: target_file_size_base × (target_file_size_multiplier ^ level),
// capped at 2GB per file.
func calculateWorstCaseCompactionIO(fromLevel int, targetFileSizeBase, targetFileSizeMultiplier int, maxCompactionBytesMB int) float64 {
	// Calculate target file size for the target level (toLevel = fromLevel + 1)
	toLevel := fromLevel + 1
	targetFileSizeMB := float64(targetFileSizeBase)

	// Apply multiplier: level 1 uses base, level 2 uses base*mult, etc.
	multiplier := float64(targetFileSizeMultiplier)
	for i := 1; i < toLevel; i++ {
		targetFileSizeMB *= multiplier
	}
	// Cap at 2GB per file (matches compactor.go logic)
	if targetFileSizeMB > 2048.0 {
		targetFileSizeMB = 2048.0
	}

	// Worst-case compaction pattern:
	// - Read 2 files from source level
	// - Read 1 file from target level (overlap)
	// - Write 1 file to target level
	// Total: 4 files
	filesPerCompaction := 4.0
	worstCaseIO := filesPerCompaction * targetFileSizeMB

	// Check if max_compaction_bytes would limit this
	// max_compaction_bytes limits INPUT size, not total I/O
	maxCompactionMB := float64(maxCompactionBytesMB)
	if maxCompactionMB > 0 {
		// Input = 2 source files + 1 target file = 3 files
		inputSize := 3.0 * targetFileSizeMB
		if inputSize > maxCompactionMB {
			// Would be limited by max_compaction_bytes
			// In this case, compaction would read less, but worst-case estimate
			// assumes we hit the limit, so use max_compaction_bytes for input
			// Output is typically ~99% of input for deeper levels
			outputSize := maxCompactionMB * 0.99
			worstCaseIO = maxCompactionMB + outputSize
		}
	}

	return worstCaseIO
}

// CalculateWorstCaseSustainableRate calculates the minimum sustainable write rate
// based on buffer capacity constraint during worst-case compaction scenarios.
//
// With serialized execution (diskBusyUntil), maxBackgroundJobs compactions can queue up
// and run sequentially. During this time, flushes are blocked and writes accumulate.
//
// Formula: worst_case_rate = buffer_capacity / worst_case_duration
// Where worst_case_duration = (worst_case_per_compaction_io × maxBackgroundJobs) / io_throughput
//
// This gives the minimum sustainable rate - the rate that fills the buffer exactly
// when all worst-case compactions complete.
func (m *Metrics) CalculateWorstCaseSustainableRate(ioThroughputMBps float64, maxBackgroundJobs int, bufferCapacityMB float64, deepestLevel int, config SimConfig) float64 {
	if deepestLevel <= 0 {
		// No levels exist yet, use conservative estimate
		return ioThroughputMBps / (1.0 + 2.5)
	}

	// Calculate worst-case I/O per compaction for deepest level
	worstCasePerCompactionIO := calculateWorstCaseCompactionIO(
		deepestLevel-1, // fromLevel (deepest-1 → deepest)
		config.TargetFileSizeMB,
		config.TargetFileSizeMultiplier,
		config.MaxCompactionBytesMB,
	)

	// With maxBackgroundJobs compactions queued, total I/O scales linearly
	totalWorstCaseIO := worstCasePerCompactionIO * float64(maxBackgroundJobs)

	// Duration of worst-case burst (serialized execution)
	worstCaseDuration := totalWorstCaseIO / ioThroughputMBps

	if worstCaseDuration <= 0 {
		return ioThroughputMBps // Avoid division by zero
	}

	// Minimum sustainable rate: buffer must absorb writes during worst-case burst
	return bufferCapacityMB / worstCaseDuration
}

// CalculateMaxSustainableWriteRate calculates the maximum sustainable write rate
// based on compaction overhead observed so far (conservative estimate).
//
// Formula: max_sustainable = disk_capacity / (1 + compaction_overhead_ratio)
// Where compaction_overhead_ratio = total_compaction_bandwidth / flush_bandwidth
//
// Compaction overhead ratio represents how much disk bandwidth is consumed
// by compactions per MB/s of flush rate. For example, if overhead is 2.5x,
// then 1 MB/s flush requires 2.5 MB/s compaction bandwidth.
//
// Universal compaction typically has lower write amplification (1.5-2.0x)
// compared to leveled compaction (2.5-3.0x), so it allows higher sustainable rates.
//
// This uses cumulative averages, which may underestimate actual overhead as
// the LSM tree grows and compactions get larger. Consider using worst-case
// calculation (CalculateWorstCaseSustainableRate) for more accurate estimates.
//
// Returns conservative estimate based on typical compaction overhead.
func (m *Metrics) CalculateMaxSustainableWriteRate(ioThroughputMBps float64, maxBackgroundJobs int, compactionStyle CompactionStyle) float64 {
	// Use conservative multiplier to account for worst-case compaction sizes
	// Universal compaction: lower base overhead (1.8x vs 2.5x for leveled)
	// Conservative multiplier: 3.0x (accounts for worst-case compaction sizes)
	var baseOverhead float64
	if compactionStyle == CompactionStyleUniversal {
		baseOverhead = 1.8 // Universal compaction: lower write amplification
	} else {
		baseOverhead = 2.5 // Leveled compaction: higher write amplification
	}
	conservativeMultiplier := 3.0
	conservativeOverhead := baseOverhead * conservativeMultiplier

	// Conservative estimate: assumes worst-case compaction overhead
	// This gives an upper bound for the sustainable rate range
	return ioThroughputMBps / (1.0 + conservativeOverhead)
}

// CapThroughput ensures throughput doesn't exceed physical disk limits
// Call this after calculateThroughput in Update()
func (m *Metrics) CapThroughput(maxThroughputMBps float64) {
	// Total throughput cannot exceed physical disk bandwidth
	if m.TotalWriteThroughputMBps > maxThroughputMBps {
		// Scale down all components proportionally
		scale := maxThroughputMBps / m.TotalWriteThroughputMBps
		m.FlushThroughputMBps *= scale
		m.CompactionThroughputMBps *= scale
		for level := range m.PerLevelThroughputMBps {
			m.PerLevelThroughputMBps[level] *= scale
		}
		m.TotalWriteThroughputMBps = maxThroughputMBps
	}
}

// Update updates the timestamp and recalculates metrics
func (m *Metrics) Update(virtualTime float64, lsmTree *LSMTree, numMemtables int, diskBusyUntil float64, ioThroughputMBps float64,
	isStalled bool, stalledWriteCount int, maxBackgroundJobs int, config SimConfig) {
	m.Timestamp = virtualTime
	m.UpdateSpaceAmplification(lsmTree.TotalSizeMB, lsmTree)
	m.UpdateReadAmplification(lsmTree, numMemtables)
	m.calculateThroughput()
	m.CapThroughput(ioThroughputMBps) // Enforce physical disk limits

	// Calculate sustainable rate range
	m.MaxSustainableWriteRateMBps = m.CalculateMaxSustainableWriteRate(ioThroughputMBps, maxBackgroundJobs, config.CompactionStyle)

	// Calculate worst-case sustainable rate based on current LSM state
	// Find deepest non-empty level
	deepestLevel := 0
	for i := len(lsmTree.Levels) - 1; i >= 0; i-- {
		if lsmTree.Levels[i].FileCount > 0 || lsmTree.Levels[i].TotalSize > 0 {
			deepestLevel = i + 1 // Target level for compaction from this level
			break
		}
	}

	// Get buffer capacity from config
	bufferCapacityMB := float64(config.MaxStalledWriteMemoryMB)
	if bufferCapacityMB <= 0 {
		bufferCapacityMB = 4096.0 // Default 4GB OOM threshold
	}

	m.MinSustainableWriteRateMBps = m.CalculateWorstCaseSustainableRate(
		ioThroughputMBps,
		maxBackgroundJobs,
		bufferCapacityMB,
		deepestLevel,
		config,
	)

	// Update stall metrics
	m.IsStalled = isStalled
	m.StalledWriteCount = stalledWriteCount
	if stalledWriteCount > m.MaxStalledWriteCount {
		m.MaxStalledWriteCount = stalledWriteCount
	}
	// StallDurationSeconds is accumulated in processWrite, not here.

	// Update in-progress activities for UI display
	m.InProgressCount = len(m.inProgressWrites)
	m.InProgressDetails = make([]map[string]interface{}, 0, len(m.inProgressWrites))
	for _, w := range m.inProgressWrites {
		detail := map[string]interface{}{
			"inputMB":   w.InputMB,
			"outputMB":  w.SizeMB,
			"fromLevel": w.Level,
			"toLevel":   w.ToLevel,
		}
		m.InProgressDetails = append(m.InProgressDetails, detail)
	}
}

// Clone creates a copy of the metrics
func (m *Metrics) Clone() *Metrics {
	clone := *m
	return &clone
}
