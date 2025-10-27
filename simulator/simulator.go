package simulator

import "fmt"

// Simulator is a PURE discrete event simulator with NO concurrency primitives.
// All state is accessed single-threaded via the Step() method.
// The caller (cmd/server) manages pacing, pause/resume, and threading.
type Simulator struct {
	config                SimConfig
	lsm                   *LSMTree
	metrics               *Metrics
	queue                 *EventQueue
	virtualTime           float64
	diskBusyUntil         float64                // Virtual time when disk I/O will be free
	numImmutableMemtables int                    // Memtables waiting to flush (in addition to active)
	compactor             Compactor              // Compaction strategy
	activeCompactions     map[int]bool           // Track which levels are actively compacting
	pendingCompactions    map[int]*CompactionJob // Jobs waiting to execute (keyed by fromLevel)
}

// NewSimulator creates a new simulator
func NewSimulator(config SimConfig) (*Simulator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	sim := &Simulator{
		config:                config,
		lsm:                   lsm,
		metrics:               NewMetrics(),
		queue:                 NewEventQueue(),
		virtualTime:           0,
		diskBusyUntil:         0,
		numImmutableMemtables: 0,
		compactor:             NewLeveledCompactor(),
		activeCompactions:     make(map[int]bool),
		pendingCompactions:    make(map[int]*CompactionJob),
	}

	// Schedule initial write event
	sim.scheduleNextWrite(0)

	// Schedule initial compaction check (simulates background compaction threads)
	sim.scheduleNextCompactionCheck(0)

	return sim, nil
}

// Step advances simulation by deltaT virtual seconds.
// Processes all events up to virtualTime + deltaT.
// This is the ONLY method that advances the simulation.
func (s *Simulator) Step(deltaT float64) {
	if deltaT <= 0 {
		return
	}

	targetTime := s.virtualTime + deltaT

	// Process all events up to target time
	for !s.queue.IsEmpty() && s.queue.Peek().Timestamp() <= targetTime {
		event := s.queue.Pop()
		s.virtualTime = event.Timestamp()
		s.processEvent(event)
	}

	// Advance to target time even if no events
	s.virtualTime = targetTime

	// Update metrics with current state
	// Total memtables = 1 active + immutable ones waiting to flush
	numMemtables := 1 + s.numImmutableMemtables
	s.metrics.Update(s.virtualTime, s.lsm, numMemtables, s.diskBusyUntil, s.config.IOThroughputMBps)
}

// Reset resets the simulation to initial state
func (s *Simulator) Reset() {
	s.lsm = NewLSMTree(s.config.NumLevels, float64(s.config.MemtableFlushSizeMB))
	s.metrics = NewMetrics()
	s.queue.Clear()
	s.virtualTime = 0
	s.diskBusyUntil = 0
	s.numImmutableMemtables = 0
	s.activeCompactions = make(map[int]bool)
	s.pendingCompactions = make(map[int]*CompactionJob)

	// Schedule initial write event
	s.scheduleNextWrite(0)

	// Schedule initial compaction check
	s.scheduleNextCompactionCheck(0)
}

// UpdateConfig updates the simulation configuration
func (s *Simulator) UpdateConfig(newConfig SimConfig) error {
	if err := newConfig.Validate(); err != nil {
		return err
	}

	// Check if any parameters changed except writeRateMBps (which can be hot-updated)
	oldConfig := s.config
	oldConfig.WriteRateMBps = newConfig.WriteRateMBps // Ignore writeRateMBps in comparison
	newConfigCopy := newConfig

	needsReset := oldConfig != newConfigCopy

	s.config = newConfig

	if needsReset {
		// Structural change - recreate LSM tree and reset simulation
		s.lsm = NewLSMTree(newConfig.NumLevels, float64(newConfig.MemtableFlushSizeMB))
		s.metrics = NewMetrics()
		s.queue = NewEventQueue()
		s.virtualTime = 0
		s.diskBusyUntil = 0
		s.numImmutableMemtables = 0
		s.activeCompactions = make(map[int]bool)
		s.pendingCompactions = make(map[int]*CompactionJob)
		// Schedule initial write
		s.scheduleNextWrite(0)
		// Schedule initial compaction check
		s.scheduleNextCompactionCheck(0)
	}
	// else
	// Hot update (only writeRateMBps can be changed without reset)
	// No action needed - config already updated above

	return nil
}

// Config returns a copy of the current configuration
func (s *Simulator) Config() SimConfig {
	return s.config
}

// VirtualTime returns the current virtual time
func (s *Simulator) VirtualTime() float64 {
	return s.virtualTime
}

// Metrics returns a copy of current metrics
func (s *Simulator) Metrics() *Metrics {
	return s.metrics.Clone()
}

// GetDiskBusyUntil returns when the disk will be free
func (s *Simulator) GetDiskBusyUntil() float64 {
	return s.diskBusyUntil
}

// State returns the current LSM tree state
func (s *Simulator) State() map[string]interface{} {
	state := s.lsm.State(s.virtualTime)
	state["virtualTime"] = s.virtualTime
	state["activeCompactions"] = s.ActiveCompactions()
	return state
}

// processEvent processes a single event
func (s *Simulator) processEvent(event Event) {
	switch e := event.(type) {
	case *WriteEvent:
		s.processWrite(e)
	case *FlushEvent:
		s.processFlush(e)
	case *CompactionEvent:
		s.processCompaction(e)
	case *CompactionCheckEvent:
		s.processCompactionCheck(e)
	default:
		panic(fmt.Sprintf("unknown event type: %T", e))
	}
}

// processWrite processes a write event
func (s *Simulator) processWrite(event *WriteEvent) {
	// Check if writes should be stalled (memtable is immutable and waiting to flush)
	// In RocksDB, writes stall when numImmutableMemtables >= maxWriteBufferNumber
	if s.numImmutableMemtables >= s.config.MaxWriteBufferNumber {
		// Write stall! Reschedule this write for when the flush might be done
		// Add a small delay (100ms) to avoid tight loop
		stallTime := s.virtualTime + 0.1
		s.queue.Push(NewWriteEvent(stallTime, event.SizeMB()))
		return
	}

	// Add write to memtable (track creation time)
	s.lsm.AddWrite(event.SizeMB(), s.virtualTime)
	s.metrics.RecordUserWrite(event.SizeMB())

	// Check if flush is needed (size-based OR time-based)
	// Only schedule flush if we don't already have max immutable memtables
	if s.lsm.NeedsFlush(s.virtualTime, s.config.MemtableFlushTimeoutSec) && s.numImmutableMemtables < s.config.MaxWriteBufferNumber {
		// Memtable is full - "freeze" it and create a new active one
		// This simulates RocksDB's behavior: current memtable becomes immutable,
		// a new active memtable is created, and the immutable one flushes in background
		sizeMB := s.lsm.MemtableCurrentSize
		s.numImmutableMemtables++ // One more immutable memtable

		// IMMEDIATELY reset the active memtable (simulate creating a new one)
		// New writes will now go to this fresh memtable
		s.lsm.MemtableCurrentSize = 0
		s.lsm.MemtableCreatedAt = s.virtualTime

		// Calculate flush duration: time to write memtable to disk at I/O throughput
		ioTimeSec := sizeMB / s.config.IOThroughputMBps
		seekTimeSec := s.config.IOLatencyMs / 1000.0
		flushDuration := ioTimeSec + seekTimeSec

		// Flush can only start when disk is free
		flushStartTime := max(s.virtualTime, s.diskBusyUntil)
		flushCompleteTime := flushStartTime + flushDuration

		// Reserve disk bandwidth
		s.diskBusyUntil = flushCompleteTime

		// Track this write as in-progress for throughput calculation
		s.metrics.StartWrite(sizeMB, sizeMB, flushStartTime, flushCompleteTime, -1, 0) // Flush: memtable → L0

		// Schedule flush event with the SIZE that was frozen (not current memtable)
		s.queue.Push(NewFlushEvent(flushCompleteTime, flushStartTime, sizeMB))
	}

	// Schedule next write
	s.scheduleNextWrite(s.virtualTime)
}

// processFlush processes a flush event
func (s *Simulator) processFlush(event *FlushEvent) {
	// Flush the immutable memtable (with the size that was frozen)
	// NOT the current active memtable!
	frozenSizeMB := event.SizeMB()
	if frozenSizeMB == 0 {
		return
	}

	// Create the L0 SST file with the frozen size
	file := s.lsm.CreateSSTFile(0, frozenSizeMB, s.virtualTime)

	// One less immutable memtable
	s.numImmutableMemtables--
	if s.numImmutableMemtables < 0 {
		s.numImmutableMemtables = 0 // Safety check
	}

	// Move from in-progress to completed
	s.metrics.CompleteWrite(event.Timestamp(), -1) // -1 = flush
	s.metrics.RecordFlush(file.SizeMB, event.StartTime(), event.Timestamp())

	// Compactions are handled by periodic CompactionCheckEvent, not triggered by flushes
}

// processCompaction processes a compaction event
func (s *Simulator) processCompaction(event *CompactionEvent) {
	fromLevel := event.FromLevel()

	// Mark level as no longer compacting
	delete(s.activeCompactions, fromLevel)

	// Retrieve the compaction job
	job, ok := s.pendingCompactions[fromLevel]
	if !ok {
		fmt.Printf("[ERROR] No pending compaction job for L%d\n", fromLevel)
		return
	}
	delete(s.pendingCompactions, fromLevel)

	// Execute the compaction using the compactor interface
	inputSize, outputSize := s.compactor.ExecuteCompaction(job, s.lsm, s.config, s.virtualTime)

	if inputSize == 0 {
		return
	}

	// Move from in-progress to completed
	s.metrics.CompleteWrite(event.Timestamp(), fromLevel)
	s.metrics.RecordCompaction(inputSize, outputSize, event.StartTime(), event.Timestamp(), fromLevel)

	// DON'T immediately schedule another compaction after this one completes
	// In classic LSM, compactions are triggered by writes/flushes, not by other compactions
	// This prevents immediate chaining of L0→L1→L2→L3... compactions
}

// tryScheduleCompaction attempts to schedule a compaction based on scoring
// Returns true if a compaction was scheduled, false otherwise
func (s *Simulator) tryScheduleCompaction() bool {
	// Check if we've hit max parallel compactions
	if len(s.activeCompactions) >= s.config.MaxBackgroundJobs {
		return false
	}

	// Find the highest-scoring level that isn't already compacting
	// We need to iterate through all levels by score
	type levelScore struct {
		level int
		score float64
	}

	// Calculate scores for all levels
	scores := make([]levelScore, 0, len(s.lsm.Levels))
	for i := 0; i < len(s.lsm.Levels)-1; i++ {
		score := s.lsm.calculateCompactionScore(i, s.config)
		scores = append(scores, levelScore{level: i, score: score})
	}

	// Sort by score descending
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	// Find first eligible level (not already compacting, score > threshold)
	var levelToCompact int = -1
	for _, ls := range scores {
		// Check threshold
		threshold := 1.0
		if ls.level > 0 {
			targetLevel := s.lsm.Levels[ls.level+1]
			if targetLevel.FileCount == 0 {
				threshold = 2.0
			} else if targetLevel.FileCount < 3 {
				threshold = 1.5
			}
		}

		if ls.score > threshold && !s.activeCompactions[ls.level] {
			levelToCompact = ls.level
			break
		}
	}

	if levelToCompact < 0 {
		return false // No level needs compaction
	}

	// Debug: show scores (only for first compaction in batch)
	if len(s.activeCompactions) == 0 {
		fmt.Printf("[SCORE] Compaction check at t=%.1f (L0: %d files, %.1f MB)\n",
			s.virtualTime, s.lsm.Levels[0].FileCount, s.lsm.Levels[0].TotalSize)
		for i := 0; i < len(s.lsm.Levels); i++ {
			score := s.lsm.calculateCompactionScore(i, s.config)
			activeMarker := ""
			if s.activeCompactions[i] {
				activeMarker = " [ACTIVE]"
			}
			fmt.Printf("[SCORE]   L%d: score=%.2f, files=%d, size=%.1f MB%s\n",
				i, score, s.lsm.Levels[i].FileCount, s.lsm.Levels[i].TotalSize, activeMarker)
		}
	}

	// Find the score for the level we picked
	pickedScore := 0.0
	for _, ls := range scores {
		if ls.level == levelToCompact {
			pickedScore = ls.score
			break
		}
	}
	fmt.Printf("[SCHEDULE] Picked L%d for compaction (score=%.2f)\n",
		levelToCompact, pickedScore)

	// Pick files to compact
	job := s.compactor.PickCompaction(levelToCompact, s.lsm, s.config)
	if job == nil {
		fmt.Printf("[SCHEDULE] L%d: PickCompaction returned nil job\n", levelToCompact)
		return false
	}

	fmt.Printf("[SCHEDULE] L%d→L%d: scheduling compaction with %d source files, %d target files\n",
		job.FromLevel, job.ToLevel, len(job.SourceFiles), len(job.TargetFiles))

	// Calculate input and output sizes
	var inputSize float64
	for _, f := range job.SourceFiles {
		inputSize += f.SizeMB
	}
	for _, f := range job.TargetFiles {
		inputSize += f.SizeMB
	}

	// Apply reduction factor
	var reductionFactor float64
	if job.FromLevel == 0 && job.ToLevel == 1 {
		reductionFactor = s.config.CompactionReductionFactor
	} else {
		reductionFactor = 0.99 // Minimal dedup for deeper levels
	}
	outputSize := inputSize * reductionFactor

	// Calculate compaction duration: time to read input + write output
	ioTimeSec := (inputSize + outputSize) / s.config.IOThroughputMBps
	seekTimeSec := s.config.IOLatencyMs / 1000.0
	compactionDuration := ioTimeSec + seekTimeSec

	// Compaction can only start when disk is free
	compactionStartTime := max(s.virtualTime, s.diskBusyUntil)
	compactionCompleteTime := compactionStartTime + compactionDuration

	// Reserve disk bandwidth
	s.diskBusyUntil = compactionCompleteTime

	// Mark level as compacting
	s.activeCompactions[job.FromLevel] = true

	// Store the job so we can execute it when the event fires
	s.pendingCompactions[job.FromLevel] = job

	// Track this write as in-progress for throughput calculation
	s.metrics.StartWrite(inputSize, outputSize, compactionStartTime, compactionCompleteTime, job.FromLevel, job.ToLevel)

	// Schedule compaction event
	s.queue.Push(NewCompactionEvent(compactionCompleteTime, compactionStartTime, job.FromLevel, job.ToLevel, inputSize, outputSize))

	return true
}

// processCompactionCheck simulates RocksDB's background compaction threads
// Periodically checks if any levels need compaction
func (s *Simulator) processCompactionCheck(event *CompactionCheckEvent) {
	// Try to schedule compactions to fill all available slots
	// Loop until we've filled all MaxBackgroundJobs slots or no more levels need compaction
	for len(s.activeCompactions) < s.config.MaxBackgroundJobs {
		scheduled := s.tryScheduleCompaction()
		if !scheduled {
			break // No more levels need compaction
		}
	}

	// Schedule next compaction check (every 1 virtual second, simulating background threads)
	s.scheduleNextCompactionCheck(s.virtualTime)
}

// scheduleNextWrite schedules the next write event based on write rate
func (s *Simulator) scheduleNextWrite(currentTime float64) {
	// Calculate time until next write based on write rate
	// WriteRateMBps is MB/s, so time between writes depends on write size
	// For simplicity, write 1 MB at a time
	writeSizeMB := 1.0
	intervalSeconds := writeSizeMB / s.config.WriteRateMBps

	nextWriteTime := currentTime + intervalSeconds
	s.queue.Push(NewWriteEvent(nextWriteTime, writeSizeMB))
}

// scheduleNextCompactionCheck schedules the next compaction check
// This simulates RocksDB's background compaction threads that periodically check for work
func (s *Simulator) scheduleNextCompactionCheck(currentTime float64) {
	// Check every 1 second of virtual time (simulating background thread wake-ups)
	checkInterval := 1.0
	nextCheckTime := currentTime + checkInterval
	s.queue.Push(NewCompactionCheckEvent(nextCheckTime))
}

// ActiveCompactions returns a list of levels currently being compacted
func (s *Simulator) ActiveCompactions() []int {
	active := make([]int, 0, len(s.activeCompactions))
	for level := range s.activeCompactions {
		active = append(active, level)
	}
	return active
}

// Helper functions
