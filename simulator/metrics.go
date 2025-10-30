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
	WriteAmplification float64 `json:"writeAmplification"` // bytes written to disk / bytes written by user
	ReadAmplification  float64 `json:"readAmplification"`  // bytes read from disk / bytes returned to user (future)
	SpaceAmplification float64 `json:"spaceAmplification"` // disk space used / logical data size

	// Latencies
	WriteLatencyMs float64 `json:"writeLatencyMs"`
	ReadLatencyMs  float64 `json:"readLatencyMs"`

	// Cumulative counters
	TotalDataWrittenMB float64 `json:"totalDataWrittenMB"` // User writes
	TotalDataReadMB    float64 `json:"totalDataReadMB"`    // User reads (future)

	// Throughput tracking (MB/s) - smoothed via exponential moving average
	FlushThroughputMBps      float64         `json:"flushThroughputMBps"`      // Memtable flush rate (smoothed)
	CompactionThroughputMBps float64         `json:"compactionThroughputMBps"` // Total compaction write rate (smoothed)
	TotalWriteThroughputMBps float64         `json:"totalWriteThroughputMBps"` // Total disk write rate (smoothed)
	PerLevelThroughputMBps   map[int]float64 `json:"perLevelThroughputMBps"`   // Per-level compaction rates (smoothed)

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

	// Internal tracking
	totalDiskWrittenMB float64         // Total bytes written to disk (including compaction)
	logicalDataSizeMB  float64         // Estimated logical data size
	recentWrites       []WriteActivity // Recent write events for throughput calculation
	inProgressWrites   []WriteActivity // Currently executing writes (not yet completed)
	throughputWindow   float64         // Time window for throughput calculation (seconds)

	// Exponential moving average smoothing (alpha = 0.2 for ~5-sample average)
	smoothingAlpha float64 // 0.2 = smooth over ~5 samples
	isFirstSample  bool    // Track first sample to initialize EMA
}

// NewMetrics creates a new metrics tracker
func NewMetrics() *Metrics {
	return &Metrics{
		Timestamp:                0,
		WriteAmplification:       1.0,
		ReadAmplification:        1.0,
		SpaceAmplification:       1.0,
		WriteLatencyMs:           0,
		ReadLatencyMs:            0,
		TotalDataWrittenMB:       0,
		TotalDataReadMB:          0,
		FlushThroughputMBps:      0,
		CompactionThroughputMBps: 0,
		TotalWriteThroughputMBps: 0,
		PerLevelThroughputMBps:   make(map[int]float64),
		CompactionsSinceUpdate:   make(map[int]CompactionStats),
		totalDiskWrittenMB:       0,
		logicalDataSizeMB:        0,
		recentWrites:             make([]WriteActivity, 0),
		inProgressWrites:         make([]WriteActivity, 0),
		throughputWindow:         5.0,  // 5-second sliding window
		smoothingAlpha:           0.2,  // Smooth over ~5 samples
		isFirstSample:            true, // Initialize EMA with first sample
		StalledWriteCount:        0,
		MaxStalledWriteCount:      0,
		StallDurationSeconds:     0,
		IsStalled:                 false,
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
func (m *Metrics) RecordCompaction(inputSizeMB, outputSizeMB, startTime, endTime float64, fromLevel int, inputFileCount, outputFileCount int) {
	// Compaction reads input files and writes output files
	m.totalDiskWrittenMB += outputSizeMB

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
func (m *Metrics) UpdateSpaceAmplification(diskSpaceMB float64) {
	if m.logicalDataSizeMB > 0 {
		m.SpaceAmplification = diskSpaceMB / m.logicalDataSizeMB
	} else {
		m.SpaceAmplification = 1.0
	}
}

// updateWriteAmplification recalculates write amplification
func (m *Metrics) updateWriteAmplification() {
	if m.TotalDataWrittenMB > 0 {
		m.WriteAmplification = m.totalDiskWrittenMB / m.TotalDataWrittenMB
	} else {
		m.WriteAmplification = 1.0
	}
}

// UpdateReadAmplification calculates read amplification based on LSM structure
func (m *Metrics) UpdateReadAmplification(lsmTree *LSMTree, numMemtables int) {
	// Read amplification = number of places to check for a key
	// - All memtables (active + immutable)
	// - All L0 files (L0 is unsorted/tiered, must check all)
	// - 1 file per level in L1+ (sorted levels, binary search)
	l0FileCount := 0
	numLevels := len(lsmTree.Levels)
	if numLevels > 0 {
		l0FileCount = lsmTree.Levels[0].FileCount
	}

	m.ReadAmplification = float64(numMemtables + l0FileCount + (numLevels - 1))

	// Floor of 1.0 (at least check memtable)
	if m.ReadAmplification < 1.0 {
		m.ReadAmplification = 1.0
	}
}

// calculateThroughput calculates INSTANTANEOUS write throughput
// Shows what's actively being written RIGHT NOW, not historical average
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

	// Calculate instantaneous throughput: sum bandwidth of all active writes
	var flushBandwidth, compactionBandwidth float64
	perLevelBandwidth := make(map[int]float64)

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
		bandwidth := w.SizeMB / writeDuration

		// Accumulate bandwidth for active writes
		if w.Level == -1 {
			// Flush
			flushBandwidth += bandwidth
		} else {
			// Compaction
			compactionBandwidth += bandwidth
			perLevelBandwidth[w.Level] += bandwidth
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
	isStalled bool, stalledWriteCount int) {
	m.Timestamp = virtualTime
	m.UpdateSpaceAmplification(lsmTree.TotalSizeMB)
	m.UpdateReadAmplification(lsmTree, numMemtables)
	m.calculateThroughput()
	m.CapThroughput(ioThroughputMBps) // Enforce physical disk limits

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
