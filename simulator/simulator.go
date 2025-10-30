package simulator

import (
	"fmt"
	"math"
)

// ActiveCompactionInfo tracks details of an in-progress compaction
type ActiveCompactionInfo struct {
	FromLevel       int  `json:"fromLevel"`
	ToLevel         int  `json:"toLevel"`
	SourceFileCount int  `json:"sourceFileCount"`
	TargetFileCount int  `json:"targetFileCount"`
	IsIntraL0       bool `json:"isIntraL0"`
}

// Simulator is a PURE discrete event simulator with NO concurrency primitives.
// All state is accessed single-threaded via the Step() method.
// The caller (cmd/server) manages pacing, pause/resume, and threading.
type Simulator struct {
	config                 SimConfig
	lsm                    *LSMTree
	metrics                *Metrics
	queue                  *EventQueue
	virtualTime            float64
	diskBusyUntil          float64                 // Virtual time when disk I/O will be free
	numImmutableMemtables  int                     // Memtables waiting to flush (in addition to active)
	immutableMemtableSizes []float64               // Sizes (MB) of immutable memtables waiting to flush
	compactor              Compactor               // Compaction strategy
	activeCompactions      map[int]bool            // Track which levels are actively compacting
	activeCompactionInfos  []*ActiveCompactionInfo // Detailed info about active compactions
	pendingCompactions     map[int]*CompactionJob  // Jobs waiting to execute (keyed by fromLevel)
	isWriteStalled         bool                    // Whether writes are currently stalled
	stallStartTime         float64                 // When the current stall started (0 if not stalled)

	// Event logging callback (optional, for UI/debugging)
	LogEvent func(msg string)
}

// NewSimulator creates a new simulator
func NewSimulator(config SimConfig) (*Simulator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	sim := &Simulator{
		config:                 config,
		lsm:                    lsm,
		metrics:                NewMetrics(),
		queue:                  NewEventQueue(),
		virtualTime:            0,
		diskBusyUntil:          0,
		numImmutableMemtables:  0,
		immutableMemtableSizes: make([]float64, 0),
		compactor:              NewLeveledCompactor(config.RandomSeed),
		activeCompactions:      make(map[int]bool),
		activeCompactionInfos:  make([]*ActiveCompactionInfo, 0),
		pendingCompactions:     make(map[int]*CompactionJob),
		isWriteStalled:         false,
		stallStartTime:         0,
	}

	// Note: Simulator starts in "dormant" state with no events scheduled
	// Call PrepareToRun() before running, or call Reset() to get a ready-to-run simulator
	return sim, nil
}

// ensureEventsScheduled ensures the simulation has the necessary recurring events
// Called internally after reset or when starting/resuming
func (s *Simulator) ensureEventsScheduled() {
	// Clear the queue and schedule fresh events
	// This is simple, correct, and not performance-critical (called rarely)
	s.queue.Clear()

	// Schedule write events (if rate > 0)
	if s.config.WriteRateMBps > 0 {
		s.scheduleNextWrite(s.virtualTime)
	}

	// Always schedule compaction checks
	s.scheduleNextCompactionCheck(s.virtualTime)

	fmt.Printf("[INIT] Scheduled initial events at t=%.1f (write_rate=%.1f MB/s)\n",
		s.virtualTime, s.config.WriteRateMBps)
}

// Step advances the simulation by one UI update interval.
// The actual amount of virtual time advanced is determined by SimulationSpeedMultiplier.
// This is the ONLY method that advances the simulation.
func (s *Simulator) Step() {
	// Invariant check: Queue should never be empty after initialization
	// WriteEvent and CompactionCheckEvent are self-perpetuating
	if s.queue.IsEmpty() {
		panic("BUG: Event queue is empty! Self-perpetuating events (WriteEvent, CompactionCheckEvent) should keep it populated.")
	}

	// Base step size: 1.0 second of virtual time per iteration
	// The UI doesn't need to know about virtual time - we control it here
	baseStepSeconds := 1.0

	// Apply simulation speed multiplier - process multiple steps per call
	speedMultiplier := s.config.SimulationSpeedMultiplier
	if speedMultiplier < 1 {
		speedMultiplier = 1
	}

	for i := 0; i < speedMultiplier; i++ {
		targetTime := s.virtualTime + baseStepSeconds

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
		// Count stalled writes (WriteEvents in queue that are rescheduled due to stall)
		stalledCount := s.countStalledWrites()
		s.metrics.Update(s.virtualTime, s.lsm, numMemtables, s.diskBusyUntil, s.config.IOThroughputMBps,
			s.isWriteStalled, stalledCount)
	}

	// Log queue size periodically (every 100 seconds of virtual time)
	if int(s.virtualTime)%100 == 0 && int(s.virtualTime) > 0 {
		fmt.Printf("[QUEUE] t=%.1f: queue size=%d, write_rate=%.1f MB/s\n",
			s.virtualTime, s.queue.Len(), s.config.WriteRateMBps)
	}
}

// Reset resets the simulation to initial state and schedules events
func (s *Simulator) Reset() {
	s.lsm = NewLSMTree(s.config.NumLevels, float64(s.config.MemtableFlushSizeMB))
	s.metrics = NewMetrics()
	s.queue.Clear()
	s.virtualTime = 0
	s.diskBusyUntil = 0
	s.numImmutableMemtables = 0
	s.immutableMemtableSizes = make([]float64, 0)
	s.activeCompactions = make(map[int]bool)
	s.activeCompactionInfos = make([]*ActiveCompactionInfo, 0)
	s.pendingCompactions = make(map[int]*CompactionJob)
	s.isWriteStalled = false
	s.stallStartTime = 0

	// Pre-populate LSM with initial data if configured
	if s.config.InitialLSMSizeMB > 0 {
		s.populateInitialLSM()
	}

	// Schedule events so simulator is ready to run
	s.ensureEventsScheduled()
}

// populateInitialLSM pre-populates the LSM tree with data to skip warmup phase
func (s *Simulator) populateInitialLSM() {
	fmt.Printf("[INIT] Populating LSM with %d MB initial data\n", s.config.InitialLSMSizeMB)

	targets := s.lsm.calculateLevelTargets(s.config)
	totalTarget := 0.0
	for i := 1; i < len(targets); i++ { // Skip L0
		totalTarget += targets[i]
	}

	fmt.Printf("[INIT] Total level targets: %.1f MB\n", totalTarget)

	// If total target is 0 or initial size is too large, just put everything in last level
	if totalTarget == 0 || float64(s.config.InitialLSMSizeMB) > totalTarget*2 {
		fmt.Printf("[INIT] Putting all data in last level (L%d)\n", len(s.lsm.Levels)-1)
		s.populateLevel(len(s.lsm.Levels)-1, float64(s.config.InitialLSMSizeMB))
		return
	}

	// Distribute data proportionally across levels based on their targets
	remainingSize := float64(s.config.InitialLSMSizeMB)
	for level := 1; level < len(s.lsm.Levels); level++ {
		if remainingSize <= 0 {
			break
		}

		// Calculate this level's share
		levelShare := targets[level] / totalTarget
		levelSize := float64(s.config.InitialLSMSizeMB) * levelShare

		// Don't exceed target (want balanced LSM, not over-full)
		if levelSize > targets[level] {
			levelSize = targets[level]
		}

		// Don't exceed remaining size
		if levelSize > remainingSize {
			levelSize = remainingSize
		}

		if levelSize > 0 {
			s.populateLevel(level, levelSize)
			remainingSize -= levelSize
		}
	}

	// If there's still remaining size (e.g., targets were too small), put it in last level
	if remainingSize > 0 {
		lastLevel := len(s.lsm.Levels) - 1
		fmt.Printf("[INIT] Placing remaining %.1f MB in L%d\n", remainingSize, lastLevel)
		s.populateLevel(lastLevel, remainingSize)
	}

	fmt.Printf("[INIT] Population complete. LSM total size: %.1f MB\n", s.lsm.TotalSizeMB)
}

// populateLevel adds files to a level to reach the target size
func (s *Simulator) populateLevel(level int, sizeMB float64) {
	fmt.Printf("[INIT] Populating L%d with %.1f MB\n", level, sizeMB)
	// Calculate target file size for this level
	fileSize := float64(s.config.TargetFileSizeMB) * math.Pow(float64(s.config.TargetFileSizeMultiplier), float64(level-1))
	if fileSize > 2048 {
		fileSize = 2048 // Cap at 2GB
	}

	// Add files until we reach target size
	remainingSize := sizeMB
	for remainingSize > 0 {
		currentFileSize := fileSize
		if currentFileSize > remainingSize {
			currentFileSize = remainingSize
		}

		// CreateSSTFile already adds the file to the level and updates TotalSizeMB
		s.lsm.CreateSSTFile(level, currentFileSize, 0) // Created at t=0
		remainingSize -= currentFileSize
	}
}

// UpdateConfig updates the simulation configuration
func (s *Simulator) UpdateConfig(newConfig SimConfig) error {
	if err := newConfig.Validate(); err != nil {
		return err
	}

	// Save original values before checking for changes
	originalWriteRate := s.config.WriteRateMBps
	originalSpeedMultiplier := s.config.SimulationSpeedMultiplier

	// Check if any static parameters changed (dynamic params: writeRateMBps, simulationSpeedMultiplier)
	oldConfig := s.config
	oldConfig.WriteRateMBps = newConfig.WriteRateMBps                         // Ignore dynamic params
	oldConfig.SimulationSpeedMultiplier = newConfig.SimulationSpeedMultiplier // Ignore dynamic params
	newConfigCopy := newConfig

	needsReset := oldConfig != newConfigCopy

	// Log dynamic config changes
	rateChangedFromZero := originalWriteRate <= 0 && newConfig.WriteRateMBps > 0

	if originalWriteRate != newConfig.WriteRateMBps {
		fmt.Printf("[CONFIG] Write rate changed: %.1f → %.1f MB/s (t=%.1f)\n",
			originalWriteRate, newConfig.WriteRateMBps, s.virtualTime)
	}
	if originalSpeedMultiplier != newConfig.SimulationSpeedMultiplier {
		fmt.Printf("[CONFIG] Speed multiplier changed: %d → %d (t=%.1f)\n",
			originalSpeedMultiplier, newConfig.SimulationSpeedMultiplier, s.virtualTime)
	}

	s.config = newConfig

	if needsReset {
		fmt.Printf("[CONFIG] Static config changed - resetting simulation (t=%.1f)\n", s.virtualTime)
		s.Reset()
	} else if rateChangedFromZero {
		// Special case: rate changed from 0 to non-zero without reset
		// Need to kick-start write events
		fmt.Printf("[CONFIG] Re-scheduling events (rate was 0, now %.1f MB/s)\n", newConfig.WriteRateMBps)
		s.ensureEventsScheduled()
	}

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

// IsQueueEmpty returns true if the event queue is empty
func (s *Simulator) IsQueueEmpty() bool {
	return s.queue.IsEmpty()
}

// State returns the current LSM tree state
func (s *Simulator) State() map[string]interface{} {
	state := s.lsm.State(s.virtualTime, s.config)
	state["virtualTime"] = s.virtualTime
	state["activeCompactions"] = s.ActiveCompactions()
	state["activeCompactionInfos"] = s.activeCompactionInfos
	state["numImmutableMemtables"] = s.numImmutableMemtables
	state["immutableMemtableSizesMB"] = s.immutableMemtableSizes
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
//
// FIDELITY: RocksDB Reference - Write path and write stall logic
// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_write.cc#L1432-L1600
//
// C++ snippet from DBImpl::PreprocessWrite():
//
//	```cpp
//	// Check if we need to delay or stop
//	if (write_controller_.IsStopped()) {
//	  return Status::Incomplete("Write stalled");
//	}
//	// Check memtable count
//	if (cfd->imm()->NumNotFlushed() >= cfd->ioptions()->max_write_buffer_number) {
//	  // Too many immutable memtables - stall writes
//	  write_thread_.BeginWriteStall();
//	  return Status::Incomplete("Write buffer full");
//	}
//	```
//
// FIDELITY: ✓ Write stall trigger matches RocksDB (numImmutableMemtables >= max_write_buffer_number)
// FIDELITY: ⚠️ SIMPLIFIED - We reschedule with fixed 1ms delay instead of blocking thread
//
//	RocksDB behavior:
//	  - Blocks the write thread (sleeps)
//	  - Checks every 1ms if stall condition cleared (kDelayInterval = 1001 microseconds)
//	  - Applies ONLY to user writes, not compactions
//
//	Our behavior:
//	  - Re-queue the write event (discrete event simulation)
//	  - Check every 1ms of virtual time (0.001 seconds)
//	  - Same effect: writes slow down when memtables pile up
func (s *Simulator) processWrite(event *WriteEvent) {
	// Write stall check - matches RocksDB's max_write_buffer_number limit
	if s.numImmutableMemtables >= s.config.MaxWriteBufferNumber {
		// Write stall! Reschedule this write for 1ms later (matches RocksDB's check interval)
		// Only log on state transition (entering stall) to avoid log spam
		if !s.isWriteStalled {
			s.isWriteStalled = true
			s.stallStartTime = s.virtualTime
			s.logEvent("[t=%.1fs] WRITE STALL: %d immutable memtables (max=%d), writes delayed",
				s.virtualTime, s.numImmutableMemtables, s.config.MaxWriteBufferNumber)
		}
		stallTime := s.virtualTime + 0.001 // 1ms = 0.001 seconds
		s.queue.Push(NewWriteEvent(stallTime, event.SizeMB()))
		return
	}

	// Stall cleared - log if we were previously stalled
	if s.isWriteStalled {
		s.isWriteStalled = false
		duration := s.virtualTime - s.stallStartTime
		// Accumulate stall duration in metrics
		s.metrics.StallDurationSeconds += duration
		s.logEvent("[t=%.1fs] WRITE STALL CLEARED: %d immutable memtables (max=%d), writes resuming (stall duration: %.3fs)",
			s.virtualTime, s.numImmutableMemtables, s.config.MaxWriteBufferNumber, duration)
		s.stallStartTime = 0
	}

	// Add write to memtable
	s.lsm.AddWrite(event.SizeMB(), s.virtualTime)
	s.metrics.RecordUserWrite(event.SizeMB())

	// Check if flush is needed (size-based)
	// FIDELITY: ✓ Flush trigger matches RocksDB's write_buffer_size check (see lsm.go:NeedsFlush)
	// FIDELITY: ✓ "Switch memtable" behavior matches RocksDB (freeze current, create new active)
	//
	// RocksDB Reference: DBImpl::HandleWriteBufferManagerFlush()
	// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_write.cc#L1820-L1850
	//
	// Only schedule flush if we don't already have max immutable memtables
	if s.lsm.NeedsFlush() && s.numImmutableMemtables < s.config.MaxWriteBufferNumber {
		// Memtable is full - "freeze" it (SwitchMemtable in RocksDB)
		// Current memtable becomes immutable, new active memtable is created,
		// and immutable one will flush to L0 in background
		sizeMB := s.lsm.MemtableCurrentSize
		s.numImmutableMemtables++                                           // One more immutable memtable
		s.immutableMemtableSizes = append(s.immutableMemtableSizes, sizeMB) // Track its size

		// IMMEDIATELY reset the active memtable (simulate creating a new one)
		// New writes will now go to this fresh memtable
		s.lsm.MemtableCurrentSize = 0
		s.lsm.MemtableCreatedAt = s.virtualTime

		// Calculate flush duration: time to write memtable to disk
		// FIDELITY: ⚠️ SIMPLIFIED - Disk I/O modeling
		//
		// RocksDB I/O system (we don't model all of this):
		//   - RateLimiter: Optional global I/O rate limit (default: disabled)
		//   - WriteController: Automatic write throttling (32 MB/s when behind)
		//   - WritableFileWriter: Buffered writes with fsync control
		//   - I/O prioritization: Low-priority compactions don't starve reads
		//
		// Our token bucket model:
		//   - duration = (data_size / throughput) + latency
		//   - diskBusyUntil = single shared resource (all I/O contends)
		//   - Captures: I/O contention between flush and compaction
		//   - Missing: Dynamic write throttling, I/O prioritization
		//   - Impact: Minor - we model the dominant effect (disk saturation)
		ioTimeSec := sizeMB / s.config.IOThroughputMBps
		seekTimeSec := s.config.IOLatencyMs / 1000.0
		flushDuration := ioTimeSec + seekTimeSec

		// Flush can only start when disk is free (token bucket model)
		flushStartTime := max(s.virtualTime, s.diskBusyUntil)
		flushCompleteTime := flushStartTime + flushDuration

		// Reserve disk bandwidth (advance token bucket)
		s.diskBusyUntil = flushCompleteTime

		// Track this write as in-progress for throughput calculation
		s.metrics.StartWrite(sizeMB, sizeMB, flushStartTime, flushCompleteTime, -1, 0) // Flush: memtable → L0

		// Schedule flush event with the SIZE that was frozen (not current memtable)
		s.queue.Push(NewFlushEvent(flushCompleteTime, flushStartTime, sizeMB))
	}

	// Schedule next write
	s.scheduleNextWrite(s.virtualTime)
}

// processFlush processes a flush event (memtable → L0 SST file)
//
// FIDELITY: RocksDB Reference - Flush completion
// https://github.com/facebook/rocksdb/blob/main/db/flush_job.cc#L380-L450
//
// C++ snippet from FlushJob::WriteLevel0Table():
//
//	```cpp
//	Status FlushJob::WriteLevel0Table() {
//	  // ... write memtable contents to SST file ...
//	  meta->fd.file_size = builder->FileSize();
//	  meta->marked_for_compaction = builder->IsMarkedForCompaction();
//	  // Add file to L0
//	  edit_->AddFile(/* level */ 0, meta);
//	  return Status::OK();
//	}
//	```
//
// FIDELITY: ✓ Creates L0 file with frozen memtable size (matches RocksDB)
// FIDELITY: ✓ FIFO queue for immutable memtables (oldest flushed first)
// FIDELITY: ⚠️ DESIGN CHOICE - We don't trigger compactions on flush completion
//
//	RocksDB approach (event-driven):
//	  - Flush completion calls MaybeScheduleFlushOrCompaction()
//	  - Immediately checks if new L0 file triggers compaction
//	  - More responsive (sub-second reaction time)
//
//	Our approach (periodic checks):
//	  - CompactionCheckEvent fires every 1 second
//	  - Simpler: avoids complex event chains
//	  - Still faithful: 1-second granularity is negligible
//	    (flush takes ~0.1s, compactions take seconds/minutes)
//	  - Same long-term behavior: compactions still triggered when needed
func (s *Simulator) processFlush(event *FlushEvent) {
	// Flush the immutable memtable (with the size that was frozen)
	// NOT the current active memtable!
	frozenSizeMB := event.SizeMB()
	if frozenSizeMB == 0 {
		return
	}

	// Create the L0 SST file with the frozen size
	file := s.lsm.CreateSSTFile(0, frozenSizeMB, s.virtualTime)

	// One less immutable memtable (remove the first one - FIFO)
	s.numImmutableMemtables--
	if s.numImmutableMemtables < 0 {
		s.numImmutableMemtables = 0 // Safety check
	}
	if len(s.immutableMemtableSizes) > 0 {
		// Avoid memory leak: copy to new slice instead of re-slicing
		// Re-slicing (x = x[1:]) keeps underlying array, causing memory leak
		s.immutableMemtableSizes = append([]float64(nil), s.immutableMemtableSizes[1:]...)
	}

	// Move from in-progress to completed
	s.metrics.CompleteWrite(event.Timestamp(), -1) // -1 = flush
	s.metrics.RecordFlush(file.SizeMB, event.StartTime(), event.Timestamp())

	// Compactions are handled by periodic CompactionCheckEvent, not triggered by flushes
	// This is acceptable - RocksDB also uses background threads that wake up periodically
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

	// Remove from activeCompactionInfos
	newInfos := make([]*ActiveCompactionInfo, 0, len(s.activeCompactionInfos)-1)
	for _, info := range s.activeCompactionInfos {
		if info.FromLevel != fromLevel || info.ToLevel != job.ToLevel {
			newInfos = append(newInfos, info)
		}
	}
	s.activeCompactionInfos = newInfos

	// Reduce CompactingSize and file counts now that compaction is complete
	var sourceSize float64
	for _, f := range job.SourceFiles {
		sourceSize += f.SizeMB
	}
	s.lsm.Levels[fromLevel].CompactingSize -= sourceSize
	if s.lsm.Levels[fromLevel].CompactingSize < 0 {
		s.lsm.Levels[fromLevel].CompactingSize = 0 // Safety check
	}

	// Reduce source level file count
	s.lsm.Levels[fromLevel].CompactingFileCount -= len(job.SourceFiles)
	if s.lsm.Levels[fromLevel].CompactingFileCount < 0 {
		s.lsm.Levels[fromLevel].CompactingFileCount = 0 // Safety check
	}

	// Reduce target level file count
	if job.ToLevel < len(s.lsm.Levels) {
		s.lsm.Levels[job.ToLevel].TargetCompactingFiles -= len(job.TargetFiles)
		if s.lsm.Levels[job.ToLevel].TargetCompactingFiles < 0 {
			s.lsm.Levels[job.ToLevel].TargetCompactingFiles = 0 // Safety check
		}
	}

	// Execute the compaction using the compactor interface
	inputSize, outputSize, outputFileCount := s.compactor.ExecuteCompaction(job, s.lsm, s.config, s.virtualTime)

	if inputSize == 0 {
		return
	}

	// Move from in-progress to completed
	s.metrics.CompleteWrite(event.Timestamp(), fromLevel)
	inputFileCount := len(job.SourceFiles) + len(job.TargetFiles)
	s.metrics.RecordCompaction(inputSize, outputSize, event.StartTime(), event.Timestamp(), fromLevel, inputFileCount, outputFileCount)

	// DON'T immediately schedule another compaction after this one completes
	// Compactions are scheduled by periodic CompactionCheckEvent (background threads)
	// This matches RocksDB's behavior: compaction completion doesn't directly trigger
	// another compaction; the background scheduler checks periodically.
}

// tryScheduleCompaction tries to schedule a compaction if resources are available
//
// RocksDB Reference: DBImpl::BackgroundCompaction() and PickCompaction()
// See: db/db_impl/db_impl_compaction_flush.cc
//
// High-fidelity implementation of RocksDB's compaction scheduling:
// - Respects max_background_jobs parallelism limit
// - Picks highest-scoring level (most urgent based on size/file count)
// - Schedules compaction job for execution when disk becomes available
func (s *Simulator) tryScheduleCompaction() bool {
	// Check if we've hit max parallel compactions
	// RocksDB's max_background_jobs limits concurrent compaction threads
	if len(s.activeCompactions) >= s.config.MaxBackgroundJobs {
		return false
	}

	// Find the highest-scoring level that isn't already compacting
	// RocksDB uses VersionStorageInfo::ComputeCompactionScore() to prioritize
	// Higher score = more urgent (either too many L0 files or level exceeds target)
	type levelScore struct {
		level int
		score float64
	}

	// Calculate total_downcompact_bytes for accurate scoring
	totalDowncompactBytes := calculateTotalDowncompactBytes(s.lsm, s.config)

	// Calculate scores for all levels
	scores := make([]levelScore, 0, len(s.lsm.Levels))
	for i := 0; i < len(s.lsm.Levels)-1; i++ {
		score := s.lsm.calculateCompactionScore(i, s.config, totalDowncompactBytes)
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

	// Find first eligible level (not already compacting, target not too busy, score > threshold)
	var levelToCompact int = -1
	for _, ls := range scores {
		// Skip if source level is already compacting
		if s.activeCompactions[ls.level] {
			continue
		}

		// FIDELITY: ⚠️ SIMPLIFIED - Overlap-based compaction throttling
		//
		// RocksDB Reference: CompactionPicker::RangeOverlapWithCompaction()
		// https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_picker.cc#L277-L305
		//
		// C++ snippet from CompactionPicker::RangeOverlapWithCompaction():
		//   ```cpp
		//   bool CompactionPicker::RangeOverlapWithCompaction(
		//       const Slice& smallest_user_key, const Slice& largest_user_key,
		//       int level) const {
		//     const Comparator* ucmp = icmp_->user_comparator();
		//     for (Compaction* c : compactions_in_progress_) {
		//       if (c->output_level() == level &&
		//           ucmp->Compare(c->GetLargestUserKey(), smallest_user_key) > 0 &&
		//           ucmp->Compare(c->GetSmallestUserKey(), largest_user_key) < 0) {
		//         return true;  // Overlaps!
		//       }
		//     }
		//     return false;
		//   }
		//   ```
		//
		// RocksDB prevents starting a new compaction if key ranges overlap with
		// in-progress compactions at the target level. We simulate this WITHOUT
		// tracking actual keys by using statistical "resource contention":
		//
		// - Track how many files at target level are being compacted
		// - Don't start if >50% of target level files are already busy
		// - This simulates the worst-case where our exponential distribution
		//   picks many overlapping files (high contention scenario)
		//
		// FIDELITY: ✓ Behavior matches RocksDB's overlap check in spirit
		// FIDELITY: ⚠️ Uses statistical approximation instead of key-range tracking
		targetLevelIdx := ls.level + 1
		// Check target level contention (skip for intra-L0 since we'll check later)
		if targetLevelIdx < len(s.lsm.Levels) {
			targetLevel := s.lsm.Levels[targetLevelIdx]
			// Check target level contention - don't start if >50% of files are busy
			if targetLevel.FileCount > 0 && targetLevel.TargetCompactingFiles > 0 {
				contentionRatio := float64(targetLevel.TargetCompactingFiles) / float64(targetLevel.FileCount)
				if contentionRatio > 0.5 {
					// Target level too busy - skip this compaction
					fmt.Printf("[CONTENTION] t=%.1f: L%d→L%d: target level too busy (%.1f%% contention: %d/%d files busy)\n",
						s.virtualTime, ls.level, targetLevelIdx, contentionRatio*100, targetLevel.TargetCompactingFiles, targetLevel.FileCount)
					continue
				}
			}
		}

		// Check threshold (use dynamic threshold for L1+ based on target level size)
		threshold := 1.0
		if ls.level > 0 {
			targetLevel := s.lsm.Levels[targetLevelIdx]
			if targetLevel.FileCount == 0 {
				threshold = 2.0
			} else if targetLevel.FileCount < 3 {
				threshold = 1.5
			}
		}

		if ls.score > threshold {
			levelToCompact = ls.level
			break
		}
	}

	if levelToCompact < 0 {
		return false // No level needs compaction
	}

	// Debug: show scores (only for first compaction in batch)
	if len(s.activeCompactions) == 0 {
		fmt.Printf("[SCORE] Compaction check at t=%.1f (L0: %d files, %.1f MB, downcompact=%.1fMB)\n",
			s.virtualTime, s.lsm.Levels[0].FileCount, s.lsm.Levels[0].TotalSize, totalDowncompactBytes)
		for i := 0; i < len(s.lsm.Levels); i++ {
			score := s.lsm.calculateCompactionScore(i, s.config, totalDowncompactBytes)
			activeMarker := ""
			if s.activeCompactions[i] {
				activeMarker = " [ACTIVE]"
			}
			compactingMarker := ""
			if s.lsm.Levels[i].CompactingSize > 0 {
				compactingMarker = fmt.Sprintf(", compacting=%.1fMB", s.lsm.Levels[i].CompactingSize)
			}
			fmt.Printf("[SCORE]   L%d: score=%.2f, files=%d, size=%.1f MB%s%s\n",
				i, score, s.lsm.Levels[i].FileCount, s.lsm.Levels[i].TotalSize, compactingMarker, activeMarker)
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
	fmt.Printf("[SCHEDULE] t=%.1f: Picked L%d for compaction (score=%.2f)\n",
		s.virtualTime, levelToCompact, pickedScore)

	// Pick files to compact
	job := s.compactor.PickCompaction(levelToCompact, s.lsm, s.config)
	if job == nil {
		fmt.Printf("[SCHEDULE] L%d: PickCompaction returned nil job\n", levelToCompact)
		return false
	}

	fmt.Printf("[SCHEDULE] t=%.1f: L%d→L%d: scheduling compaction with %d source files, %d target files\n",
		s.virtualTime, job.FromLevel, job.ToLevel, len(job.SourceFiles), len(job.TargetFiles))

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

	// Mark level as compacting and track compacting bytes
	s.activeCompactions[job.FromLevel] = true

	// Track detailed compaction info for UI
	info := &ActiveCompactionInfo{
		FromLevel:       job.FromLevel,
		ToLevel:         job.ToLevel,
		SourceFileCount: len(job.SourceFiles),
		TargetFileCount: len(job.TargetFiles),
		IsIntraL0:       job.FromLevel == 0 && job.ToLevel == 0,
	}
	s.activeCompactionInfos = append(s.activeCompactionInfos, info)

	// Track compacting bytes and file counts for accurate score calculation and overlap detection
	// Source files are being compacted FROM this level
	var sourceSize float64
	for _, f := range job.SourceFiles {
		sourceSize += f.SizeMB
	}
	s.lsm.Levels[job.FromLevel].CompactingSize += sourceSize
	s.lsm.Levels[job.FromLevel].CompactingFileCount += len(job.SourceFiles)

	// Target files are being used as overlap targets at the TO level
	if job.ToLevel < len(s.lsm.Levels) {
		s.lsm.Levels[job.ToLevel].TargetCompactingFiles += len(job.TargetFiles)
	}

	// Store the job so we can execute it when the event fires
	s.pendingCompactions[job.FromLevel] = job

	// Track this write as in-progress for throughput calculation
	s.metrics.StartWrite(inputSize, outputSize, compactionStartTime, compactionCompleteTime, job.FromLevel, job.ToLevel)

	// Schedule compaction event
	s.queue.Push(NewCompactionEvent(compactionCompleteTime, compactionStartTime, job.FromLevel, job.ToLevel, inputSize, outputSize))

	return true
}

// processCompactionCheck simulates RocksDB's background compaction threads
//
// FIDELITY: RocksDB Reference - Background compaction scheduling
// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_compaction_flush.cc#L2761-L2850
//
// C++ snippet from DBImpl::BackgroundCallCompaction():
//
//	```cpp
//	void DBImpl::BackgroundCallCompaction() {
//	  // ... prepare environment ...
//	  while (bg_compaction_scheduled_) {
//	    BackgroundCompaction(&made_progress, &job_context, &log_buffer);
//	    // If no more work, exit loop
//	    if (!made_progress) break;
//	  }
//	  // Reschedule if more work available
//	  MaybeScheduleFlushOrCompaction();
//	}
//	```
//
// FIDELITY: ✓ Matches RocksDB's loop-until-no-work pattern
// FIDELITY: ✓ Respects MaxBackgroundJobs parallelism limit
// FIDELITY: ⚠️ DESIGN CHOICE - We use periodic checks (1s interval) instead of event-driven
//
//	RocksDB: Background threads wake on signals (flush completion, write, etc.)
//	Our simulator: Check every 1 second of virtual time
//
//	Why 1 second is reasonable:
//	  - Flush: 64MB @ 500MB/s = 0.128s (check every ~8 flushes)
//	  - Small compaction: 256MB @ 500MB/s = 0.512s (check every ~2 compactions)
//	  - Large compaction: 10GB @ 500MB/s = 20s (check 20 times during)
//	  - Worst-case delay: <1s to react to new compaction need
//	  - Impact: Negligible for understanding long-term LSM behavior
//
//	Could be tuned, but 1s provides good balance of accuracy vs. event overhead.
func (s *Simulator) processCompactionCheck(event *CompactionCheckEvent) {
	// Try to schedule compactions to fill all available slots
	// Loop until we've filled all MaxBackgroundJobs slots or no more levels need compaction
	for len(s.activeCompactions) < s.config.MaxBackgroundJobs {
		scheduled := s.tryScheduleCompaction()
		if !scheduled {
			break // No more levels need compaction
		}
	}

	// Schedule next compaction check (every 1 virtual second, simulating background thread wake-ups)
	s.scheduleNextCompactionCheck(s.virtualTime)
}

// scheduleNextWrite schedules the next write event based on write rate
func (s *Simulator) scheduleNextWrite(currentTime float64) {
	// Don't schedule writes if rate is 0 or negative
	if s.config.WriteRateMBps <= 0 {
		fmt.Printf("[WRITE] Skipping write scheduling: rate=%.1f MB/s (t=%.1f)\n",
			s.config.WriteRateMBps, currentTime)
		return
	}

	// Calculate time until next write based on write rate
	// WriteRateMBps is MB/s, so time between writes depends on write size
	// For simplicity, write 1 MB at a time
	writeSizeMB := 1.0
	intervalSeconds := writeSizeMB / s.config.WriteRateMBps

	nextWriteTime := currentTime + intervalSeconds
	s.queue.Push(NewWriteEvent(nextWriteTime, writeSizeMB))

	// Log occasionally (every 100 writes) to avoid spam
	if int(currentTime)%100 == 0 {
		fmt.Printf("[WRITE] Scheduled write at t=%.1f (interval=%.4fs, rate=%.1f MB/s)\n",
			nextWriteTime, intervalSeconds, s.config.WriteRateMBps)
	}
}

// scheduleNextCompactionCheck schedules the next compaction check
// This simulates RocksDB's background compaction threads that periodically check for work
func (s *Simulator) scheduleNextCompactionCheck(currentTime float64) {
	// Check every 1 second of virtual time (simulating background thread wake-ups)
	checkInterval := 1.0
	nextCheckTime := currentTime + checkInterval
	s.queue.Push(NewCompactionCheckEvent(nextCheckTime))
}

// countStalledWrites estimates the number of stalled write events in the queue
func (s *Simulator) countStalledWrites() int {
	if !s.isWriteStalled {
		return 0
	}
	// Count WriteEvents in queue that are scheduled after current time
	// (these are stalled writes waiting to be retried)
	// We need to peek at the queue without modifying it
	// Since we can't iterate safely, we'll estimate based on queue size
	// A more accurate count would require queue inspection, but for metrics
	// this approximation is sufficient
	queueSize := s.queue.Len()
	// Rough estimate: if we're stalled, some portion of queue is stalled writes
	// In practice, during a stall, most writes are WriteEvents waiting to retry
	return queueSize
}

// ActiveCompactions returns a list of levels currently being compacted
func (s *Simulator) ActiveCompactions() []int {
	active := make([]int, 0, len(s.activeCompactions))
	for level := range s.activeCompactions {
		active = append(active, level)
	}
	return active
}

// logEvent sends a log message to both stdout and the UI (if callback is set)
func (s *Simulator) logEvent(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	if s.LogEvent != nil {
		s.LogEvent(msg)
	}
}

// Helper functions
