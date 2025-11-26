package simulator

import (
	"fmt"
	"math"
	"math/rand"
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
	config                  SimConfig
	lsm                     *LSMTree
	metrics                 *Metrics
	queue                   *EventQueue
	virtualTime             float64
	diskBusyUntil           float64                 // Virtual time when disk I/O will be free
	numImmutableMemtables   int                     // Memtables waiting to flush (in addition to active)
	immutableMemtableSizes  []float64               // Sizes (MB) of immutable memtables waiting to flush
	compactor               Compactor               // Compaction strategy
	activeCompactionInfos   []*ActiveCompactionInfo // Detailed info about active compactions
	pendingCompactions      map[int]*CompactionJob  // Jobs waiting to execute (keyed by compaction ID, not fromLevel)
	nextCompactionID        int                     // Unique ID for each compaction job
	stallStartTime          float64                 // When the current stall started (0 if not stalled)
	stalledWriteBacklog     int                     // Number of writes waiting during stall (for OOM detection)
	nextFlushCompletionTime float64                 // When the next flush that will clear the stall completes (0 if none scheduled)
	trafficDistribution     TrafficDistribution     // Traffic distribution generator
	rng                     *rand.Rand              // Random number generator (for read path modeling and other features)

	// Event logging callback (optional, for UI/debugging)
	LogEvent func(msg string)
}

// NewSimulator creates a new simulator
func NewSimulator(config SimConfig) (*Simulator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Sync top-level WriteRateMBps to TrafficDistribution.WriteRateMBps for constant model
	if config.TrafficDistribution.Model == TrafficModelConstant {
		config.TrafficDistribution.WriteRateMBps = config.WriteRateMBps
	}

	lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))

	// Create appropriate compactor based on compaction style
	var compactor Compactor
	switch config.CompactionStyle {
	case CompactionStyleLeveled:
		compactor = NewLeveledCompactorWithOverlapDist(config.RandomSeed, config.OverlapDistribution)
	case CompactionStyleUniversal:
		compactor = NewUniversalCompactorWithOverlapDist(config.RandomSeed, config.OverlapDistribution)
	case CompactionStyleFIFO:
		compactor = NewFIFOCompactor(config.RandomSeed)
	default:
		// Default to universal compaction
		compactor = NewUniversalCompactorWithOverlapDist(config.RandomSeed, config.OverlapDistribution)
	}

	// Create traffic distribution
	trafficDist := NewTrafficDistribution(config.TrafficDistribution, config.RandomSeed)

	// Create random number generator for read path modeling
	var rng *rand.Rand
	if config.RandomSeed == 0 {
		rng = rand.New(rand.NewSource(rand.Int63()))
	} else {
		rng = rand.New(rand.NewSource(config.RandomSeed))
	}

	sim := &Simulator{
		config:                  config,
		lsm:                     lsm,
		metrics:                 NewMetrics(),
		queue:                   NewEventQueue(),
		virtualTime:             0,
		diskBusyUntil:           0,
		numImmutableMemtables:   0,
		immutableMemtableSizes:  make([]float64, 0),
		compactor:               compactor,
		activeCompactionInfos:   make([]*ActiveCompactionInfo, 0),
		pendingCompactions:      make(map[int]*CompactionJob),
		nextCompactionID:        1,
		stallStartTime:          0,
		stalledWriteBacklog:     0,
		nextFlushCompletionTime: 0,
		trafficDistribution:     trafficDist,
		rng:                     rng,
	}

	// Note: Simulator starts in "dormant" state with no events scheduled
	// Call PrepareToRun() before running, or call Reset() to get a ready-to-run simulator
	return sim, nil
}

// ensureEventsScheduled ensures the simulation has the necessary recurring events
// Called internally after reset or when starting/resuming
func (s *Simulator) ensureEventsScheduled() {
	// CRITICAL: Before clearing the queue, save any pending flush events for immutable memtables
	// This prevents losing flush events when re-scheduling (e.g., when traffic model changes)
	pendingFlushSizes := make([]float64, 0)
	if s.numImmutableMemtables > 0 {
		// Find all flush events in the queue and save their sizes
		// We'll re-schedule them after clearing the queue
		for _, event := range s.queue.Events() {
			if flushEvent, ok := event.(*FlushEvent); ok {
				pendingFlushSizes = append(pendingFlushSizes, flushEvent.SizeMB())
			}
		}
		// If we have immutable memtables but no flush events in queue, use the tracked sizes
		if len(pendingFlushSizes) == 0 && len(s.immutableMemtableSizes) > 0 {
			pendingFlushSizes = append([]float64(nil), s.immutableMemtableSizes...)
		}
	}

	// Clear the queue and schedule fresh events
	// This is simple, correct, and not performance-critical (called rarely)
	s.queue.Clear()

	// Recreate traffic distribution (in case config changed)
	s.trafficDistribution = NewTrafficDistribution(s.config.TrafficDistribution, s.config.RandomSeed)

	// Initialize time tracking for advanced traffic distribution
	if advDist, ok := s.trafficDistribution.(*AdvancedTrafficDistribution); ok {
		advDist.UpdateTime(s.virtualTime)
	}

	// Re-schedule flush events for existing immutable memtables
	// This ensures flushes aren't lost when re-scheduling events
	// Only re-schedule as many as we have immutable memtables (safety check)
	maxFlushes := s.numImmutableMemtables
	if maxFlushes > len(pendingFlushSizes) {
		maxFlushes = len(pendingFlushSizes)
	}
	for i := 0; i < maxFlushes && i < len(pendingFlushSizes); i++ {
		sizeMB := pendingFlushSizes[i]
		if sizeMB > 0 {
			// Calculate flush duration
			ioTimeSec := sizeMB / s.config.IOThroughputMBps
			seekTimeSec := s.config.IOLatencyMs / 1000.0
			flushDuration := ioTimeSec + seekTimeSec

			// Flush can only start when disk is free
			flushStartTime := max(s.virtualTime, s.diskBusyUntil)
			flushCompleteTime := flushStartTime + flushDuration

			// Reserve disk bandwidth
			s.diskBusyUntil = flushCompleteTime

			// Track this write as in-progress for throughput calculation
			s.metrics.StartWrite(sizeMB, sizeMB, flushStartTime, flushCompleteTime, -1, 0)

			// Schedule flush event
			s.queue.Push(NewFlushEvent(flushCompleteTime, flushStartTime, sizeMB))
		}
	}

	// Schedule write scheduler event (if rate > 0)
	// This continuously schedules writes at the configured rate
	writeRate := s.config.TrafficDistribution.WriteRateMBps
	if s.config.TrafficDistribution.Model == TrafficModelConstant && writeRate > 0 {
		s.scheduleNextScheduleWrite(s.virtualTime)
	} else if s.config.TrafficDistribution.Model == TrafficModelAdvancedONOFF && s.config.TrafficDistribution.BaseRateMBps > 0 {
		s.scheduleNextScheduleWrite(s.virtualTime)
	}

	// Always schedule compaction checks
	s.scheduleNextCompactionCheck(s.virtualTime)

	// Schedule read batch processing (if reads enabled and rate > 0)
	if s.config.ReadWorkload != nil && s.config.ReadWorkload.RequestsPerSec > 0 {
		s.scheduleNextScheduleRead(s.virtualTime)
	}

	writeRateStr := fmt.Sprintf("%.1f MB/s", writeRate)
	if s.config.TrafficDistribution.Model == TrafficModelAdvancedONOFF {
		writeRateStr = fmt.Sprintf("advanced (base=%.1f MB/s)", s.config.TrafficDistribution.BaseRateMBps)
	}
	fmt.Printf("[INIT] Scheduled initial events at t=%.1f (write_rate=%s)\n",
		s.virtualTime, writeRateStr)
}

// Step advances the simulation by one UI update interval.
// The actual amount of virtual time advanced is determined by SimulationSpeedMultiplier.
// This is the ONLY method that advances the simulation.
func (s *Simulator) Step() {
	// If OOM already occurred, don't process any more events
	if s.metrics.IsOOMKilled {
		return
	}

	// Invariant check: Queue should never be empty after initialization (unless write rate is 0)
	// ScheduleWriteEvent and CompactionCheckEvent are self-perpetuating
	// Exception: If write rate is 0, ScheduleWriteEvent may not be scheduled, so queue can be empty
	effectiveRate := s.getEffectiveWriteRateMBps()
	if s.queue.IsEmpty() && effectiveRate > 0 {
		// CRITICAL DEBUG: Log exact state when queue becomes empty (but only if rate > 0)
		fmt.Printf("[BUG] Queue empty at t=%.3f! WriteRate: %.1f, OOM: %v, numImmutableMemtables: %d, activeCompactions: %d\n",
			s.virtualTime, effectiveRate, s.metrics.IsOOMKilled, s.numImmutableMemtables, 0)
		panic("BUG: Event queue is empty! Self-perpetuating events (ScheduleWriteEvent, CompactionCheckEvent) should keep it populated.")
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
			// CRITICAL BUG FIX: Virtual time must NEVER go backwards
			// Use max() to ensure time is monotonic - if event was scheduled earlier but
			// processing was delayed, we don't want to set time backwards
			// This prevents time regression when events are processed out of strict order
			// (e.g., due to SimulationSpeedMultiplier processing multiple steps at once)
			s.virtualTime = max(s.virtualTime, event.Timestamp())
			s.processEvent(event)
			// If OOM occurred during event processing, stop immediately
			if s.metrics.IsOOMKilled {
				return
			}
		}

		// Advance to target time even if no events
		s.virtualTime = targetTime

		// Update metrics with current state
		// Total memtables = 1 active + immutable ones waiting to flush
		numMemtables := 1 + s.numImmutableMemtables
		// Count stalled writes (WriteEvents in queue that are rescheduled due to stall)
		isStalled := s.stallStartTime > 0
		stalledCount := s.countStalledWrites()

		// Check OOM condition periodically while stalled (not just when processing writes)
		// This ensures OOM is detected even if stalled writes are scheduled far in the future
		// Use actual queued write count (each write is 1 MB) rather than duration-based calculation
		// to account for cumulative backlog across multiple stalls
		if isStalled && s.config.MaxStalledWriteMemoryMB > 0 && !s.metrics.IsOOMKilled {
			// Calculate backlog as number of queued writes * write size (1 MB per write)
			actualBacklogMB := float64(stalledCount) * 1.0 // Each write is 1 MB

			// Also check duration-based backlog for the current stall (for logging/debugging)
			stallDuration := s.virtualTime - s.stallStartTime
			effectiveRate := s.getEffectiveWriteRateMBps()
			durationBasedBacklogMB := stallDuration * effectiveRate

			// Use the actual queued write count for OOM detection (more accurate)
			if actualBacklogMB > float64(s.config.MaxStalledWriteMemoryMB) {
				s.logEvent("[t=%.1fs] OOM KILLED: Stalled write backlog exceeded limit (%.1f MB > %d MB, queued writes: %d, current stall duration: %.2fs, duration-based estimate: %.1f MB)",
					s.virtualTime, actualBacklogMB, s.config.MaxStalledWriteMemoryMB, stalledCount, stallDuration, durationBasedBacklogMB)
				s.queue.Clear() // Stop all events
				s.metrics.IsStalled = true
				s.metrics.IsOOMKilled = true
				return
			}
		}

		s.metrics.Update(s.virtualTime, s.lsm, numMemtables, s.diskBusyUntil, s.config.IOThroughputMBps,
			isStalled, stalledCount, s.config.MaxBackgroundJobs, s.config, s.rng)

		// Invariant check: Queue should never be empty after initialization (unless OOM killed)
		// ScheduleWriteEvent and CompactionCheckEvent are self-perpetuating
		if s.queue.IsEmpty() && !s.metrics.IsOOMKilled {
			// CRITICAL DEBUG: Log exact state when queue becomes empty
			effectiveRate := s.getEffectiveWriteRateMBps()
			fmt.Printf("[BUG] Queue empty at t=%.3f (after iteration %d)! WriteRate: %.1f, OOM: %v, numImmutableMemtables: %d, activeCompactions: %d\n",
				s.virtualTime, i, effectiveRate, s.metrics.IsOOMKilled, s.numImmutableMemtables, len(s.pendingCompactions))
			panic("BUG: Event queue is empty! Self-perpetuating events (ScheduleWriteEvent, CompactionCheckEvent) should keep it populated.")
		}
	}

	// Log queue size periodically (every 100 seconds of virtual time)
	if int(s.virtualTime)%100 == 0 && int(s.virtualTime) > 0 {
		effectiveRate := s.getEffectiveWriteRateMBps()
		fmt.Printf("[QUEUE] t=%.1f: queue size=%d, write_rate=%.1f MB/s\n",
			s.virtualTime, s.queue.Len(), effectiveRate)
	}
}

// Reset resets the simulation to initial state and schedules events
func (s *Simulator) Reset() error {
	// Create a fresh simulator using the same config
	// This ensures all internal state (including compactor's activeCompactions) is fresh
	newSim, err := NewSimulator(s.config)
	if err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}

	// Preserve the LogEvent callback if it was set
	logEvent := s.LogEvent

	// Copy all fields from the new simulator
	*s = *newSim

	// Restore the LogEvent callback
	s.LogEvent = logEvent

	// Pre-populate LSM with initial data if configured
	if s.config.InitialLSMSizeMB > 0 {
		s.populateInitialLSM()
	}

	// Schedule events so simulator is ready to run
	s.ensureEventsScheduled()

	return nil
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
	originalTrafficModel := s.config.TrafficDistribution.Model
	originalSpeedMultiplier := s.config.SimulationSpeedMultiplier

	// Check if any static parameters changed (dynamic params: writeRateMBps, simulationSpeedMultiplier, trafficDistribution, readWorkload)
	oldConfig := s.config
	oldConfig.WriteRateMBps = newConfig.WriteRateMBps                         // Ignore dynamic params
	oldConfig.SimulationSpeedMultiplier = newConfig.SimulationSpeedMultiplier // Ignore dynamic params
	oldConfig.TrafficDistribution = newConfig.TrafficDistribution             // Ignore dynamic params
	oldConfig.ReadWorkload = newConfig.ReadWorkload                           // Ignore dynamic params (read metrics only)
	newConfigCopy := newConfig

	needsReset := oldConfig != newConfigCopy

	// Sync top-level WriteRateMBps to TrafficDistribution.WriteRateMBps for constant model
	// MUST do this BEFORE checking trafficDistChanged to ensure sync is detected
	if newConfig.TrafficDistribution.Model == TrafficModelConstant {
		// Always sync, even if rate didn't change (in case config was loaded with mismatch)
		if newConfig.TrafficDistribution.WriteRateMBps != newConfig.WriteRateMBps {
			newConfig.TrafficDistribution.WriteRateMBps = newConfig.WriteRateMBps
		}
	}

	// Log dynamic config changes
	rateChangedFromZero := originalWriteRate <= 0 && newConfig.WriteRateMBps > 0
	trafficModelChanged := originalTrafficModel != newConfig.TrafficDistribution.Model
	trafficDistChanged := s.config.TrafficDistribution != newConfig.TrafficDistribution

	if originalWriteRate != newConfig.WriteRateMBps {
		fmt.Printf("[CONFIG] Write rate changed: %.1f → %.1f MB/s (t=%.1f)\n",
			originalWriteRate, newConfig.WriteRateMBps, s.virtualTime)
		// If rate changed for constant model, force recreation of traffic distribution
		if newConfig.TrafficDistribution.Model == TrafficModelConstant {
			trafficDistChanged = true
		}
	}
	if trafficModelChanged || trafficDistChanged {
		if trafficModelChanged {
			fmt.Printf("[CONFIG] Traffic model changed: %s → %s (t=%.1f)\n",
				originalTrafficModel.String(), newConfig.TrafficDistribution.Model.String(), s.virtualTime)
		} else {
			fmt.Printf("[CONFIG] Traffic distribution parameters changed (t=%.1f)\n", s.virtualTime)
		}
		// Recreate traffic distribution
		s.trafficDistribution = NewTrafficDistribution(newConfig.TrafficDistribution, newConfig.RandomSeed)
	}
	if originalSpeedMultiplier != newConfig.SimulationSpeedMultiplier {
		fmt.Printf("[CONFIG] Speed multiplier changed: %d → %d (t=%.1f)\n",
			originalSpeedMultiplier, newConfig.SimulationSpeedMultiplier, s.virtualTime)
	}

	// If compaction style or overlap distribution changed, create new compactor
	overlapDistChanged := s.config.OverlapDistribution != newConfig.OverlapDistribution
	if s.config.CompactionStyle != newConfig.CompactionStyle || overlapDistChanged {
		if s.config.CompactionStyle != newConfig.CompactionStyle {
			fmt.Printf("[CONFIG] Compaction style changed: %s → %s (t=%.1f)\n",
				s.config.CompactionStyle.String(), newConfig.CompactionStyle.String(), s.virtualTime)
		}
		if overlapDistChanged {
			fmt.Printf("[CONFIG] Overlap distribution changed (t=%.1f)\n", s.virtualTime)
		}
		var compactor Compactor
		switch newConfig.CompactionStyle {
		case CompactionStyleLeveled:
			compactor = NewLeveledCompactorWithOverlapDist(newConfig.RandomSeed, newConfig.OverlapDistribution)
		case CompactionStyleUniversal:
			compactor = NewUniversalCompactorWithOverlapDist(newConfig.RandomSeed, newConfig.OverlapDistribution)
		default:
			compactor = NewUniversalCompactorWithOverlapDist(newConfig.RandomSeed, newConfig.OverlapDistribution)
		}
		s.compactor = compactor
	}

	s.config = newConfig

	if needsReset {
		fmt.Printf("[CONFIG] Static config changed - resetting simulation (t=%.1f)\n", s.virtualTime)
		if err := s.Reset(); err != nil {
			return fmt.Errorf("failed to reset simulation: %w", err)
		}
	} else if rateChangedFromZero || trafficModelChanged || trafficDistChanged {
		// Special case: rate changed from 0 to non-zero or traffic model/distribution changed without reset
		// Need to kick-start write events
		writeRate := newConfig.TrafficDistribution.WriteRateMBps
		if newConfig.TrafficDistribution.Model == TrafficModelAdvancedONOFF {
			writeRate = newConfig.TrafficDistribution.BaseRateMBps
		}
		if writeRate > 0 {
			fmt.Printf("[CONFIG] Re-scheduling events (rate was 0, now %.1f MB/s)\n", writeRate)
		}
		s.ensureEventsScheduled()
	}

	return nil
}

// getEffectiveWriteRateMBps returns the effective write rate for metrics/debugging
// For constant model: returns WriteRateMBps from TrafficDistribution
// For advanced model: returns BaseRateMBps (average rate)
func (s *Simulator) getEffectiveWriteRateMBps() float64 {
	if s.config.TrafficDistribution.Model == TrafficModelConstant {
		return s.config.TrafficDistribution.WriteRateMBps
	}
	// For advanced model, use base rate as effective rate
	return s.config.TrafficDistribution.BaseRateMBps
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

	// Add base level for universal compaction and leveled compaction with dynamic level bytes
	// FIDELITY: ✓ Unified implementation - uses appropriate method for each compaction style
	// - Universal compaction: uses calculateBaseLevel() (first non-empty level)
	// - Leveled compaction with dynamic level bytes: uses calculateDynamicBaseLevel() (based on max level size)
	if s.config.CompactionStyle == CompactionStyleUniversal {
		baseLevel := s.lsm.calculateBaseLevel()
		state["baseLevel"] = baseLevel
	} else if s.config.CompactionStyle == CompactionStyleLeveled && s.config.LevelCompactionDynamicLevelBytes {
		baseLevel := s.lsm.calculateDynamicBaseLevel(s.config)
		state["baseLevel"] = baseLevel
	}

	// Add current incoming write rate (for advanced traffic models)
	if advDist, ok := s.trafficDistribution.(*AdvancedTrafficDistribution); ok {
		state["currentIncomingRateMBps"] = advDist.GetCurrentRateMBps()
	} else {
		// For constant model, use the configured rate
		state["currentIncomingRateMBps"] = s.config.TrafficDistribution.WriteRateMBps
	}

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
	case *ScheduleWriteEvent:
		s.processScheduleWrite(e)
	case *WALWriteEvent:
		s.processWALWrite(e)
	case *ScheduleReadEvent:
		s.processScheduleRead(e)
	case *ReadBatchEvent:
		s.processReadBatch(e)
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
		// Write stall! Initialize stall state if this is the first stalled write
		isFirstStall := s.stallStartTime == 0
		if isFirstStall {
			s.stallStartTime = s.virtualTime
			s.stalledWriteBacklog = 0
			// Log only when entering stall state (not for every retry)
			s.logEvent("[t=%.1fs] WRITE STALL: %d immutable memtables (max=%d), writes delayed",
				s.virtualTime, s.numImmutableMemtables, s.config.MaxWriteBufferNumber)
		}

		// Calculate backlog based on stall duration and write rate
		// This is more accurate than counting events, especially at high simulation speeds
		stallDuration := s.virtualTime - s.stallStartTime
		effectiveRate := s.getEffectiveWriteRateMBps()
		estimatedBacklogMB := stallDuration * effectiveRate

		// Increment backlog counter for tracking
		s.stalledWriteBacklog++

		// Check OOM condition: if backlog exceeds threshold, stop simulation
		// Use actual queued write count (each write is 1 MB) for more accurate OOM detection
		// This accounts for cumulative backlog across multiple stalls
		actualBacklogMB := float64(s.countStalledWrites()) * 1.0 // Each write is 1 MB
		if s.config.MaxStalledWriteMemoryMB > 0 && actualBacklogMB > float64(s.config.MaxStalledWriteMemoryMB) {
			s.logEvent("[t=%.1fs] OOM KILLED: Stalled write backlog exceeded limit (%.1f MB > %d MB, queued writes: %d, current stall duration: %.2fs, duration-based estimate: %.1f MB)",
				s.virtualTime, actualBacklogMB, s.config.MaxStalledWriteMemoryMB, s.countStalledWrites(), stallDuration, estimatedBacklogMB)
			s.queue.Clear() // Stop all events
			s.metrics.IsStalled = true
			s.metrics.IsOOMKilled = true
			return
		}

		// Reschedule this write - use flush-aware scheduling to avoid event explosion
		// Schedule retry at next flush completion time, or fallback to 1ms if no flush scheduled
		var stallTime float64
		if s.nextFlushCompletionTime > s.virtualTime {
			// Schedule retry slightly after flush completes to ensure flush processes first
			stallTime = s.nextFlushCompletionTime + 0.0001
		} else {
			// Fallback: no flush scheduled, schedule 1ms retry (matches RocksDB's check interval)
			stallTime = s.virtualTime + 0.001 // 1ms = 0.001 seconds
		}
		// CRITICAL BUG FIX: Ensure stallTime is never in the past
		stallTime = max(stallTime, s.virtualTime)
		s.queue.Push(NewStalledWriteEvent(stallTime, event.SizeMB()))
		return
	}

	// Stall cleared - log if we were previously stalled
	if s.stallStartTime > 0 {
		duration := s.virtualTime - s.stallStartTime
		// Accumulate stall duration in metrics
		s.metrics.StallDurationSeconds += duration
		s.logEvent("[t=%.1fs] WRITE STALL CLEARED: %d immutable memtables (max=%d), writes resuming (stall duration: %.3fs, backlog cleared: %d writes)",
			s.virtualTime, s.numImmutableMemtables, s.config.MaxWriteBufferNumber, duration, s.stalledWriteBacklog)
		s.stallStartTime = 0
		s.stalledWriteBacklog = 0     // Clear backlog when stall clears
		s.nextFlushCompletionTime = 0 // No need to track flush completion time when not stalled
	}

	// Write to WAL BEFORE memtable (durability guarantee)
	// FIDELITY: RocksDB Reference - WriteToWAL happens before memtable insert
	// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_write.cc
	//
	// RocksDB always writes to WAL before memtable to ensure durability.
	// WAL writes are sequential and may include fsync() for durability.
	if s.config.EnableWAL {
		walSizeMB := event.SizeMB()

		// Calculate WAL write duration: sequential write time + optional sync
		ioTimeSec := walSizeMB / s.config.IOThroughputMBps
		walDuration := ioTimeSec

		// Add fsync latency if WALSync is enabled
		if s.config.WALSync {
			syncTimeSec := s.config.WALSyncLatencyMs / 1000.0
			walDuration += syncTimeSec
		}

		// WAL write contends for disk bandwidth
		walStartTime := max(s.virtualTime, s.diskBusyUntil)
		walCompleteTime := walStartTime + walDuration

		// Reserve disk bandwidth
		s.diskBusyUntil = walCompleteTime

		// Schedule WAL completion event
		walEvent := NewWALWriteEvent(walCompleteTime, walStartTime, walSizeMB)
		s.queue.Push(walEvent)

		// Track WAL bytes separately (NOT included in LSM write amplification)
		// RocksDB's write amplification metric measures LSM compaction overhead only,
		// not WAL writes. See: internal_stats.cc:1806-1842 (write_amp = compaction_output / flush_input)
		s.metrics.WALBytesWritten += walSizeMB
		// NOTE: WAL bytes are NOT added to totalDiskWrittenMB (which tracks flush + compaction only)

		// Track WAL write activity for disk throughput/utilization calculations
		// Use Level = -2 to distinguish WAL from flush (-1) and compactions (0+)
		s.metrics.RecordWALWrite(walStartTime, walCompleteTime, walSizeMB)
	}

	// Add write to memtable (after WAL)
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
		//   - Disk bandwidth tracked as available tokens
		//   - Operations reserve tokens, refund on completion
		//   - Captures: I/O contention between flush and compaction
		//   - Missing: Dynamic write throttling, I/O prioritization
		//   - Impact: Minor - we model the dominant effect (disk saturation)
		// Calculate flush duration using ADDITIVE MODEL
		// Flush process: compress memtable → write compressed data to L0 file
		// Operations are sequential: compress + write I/O + seek
		var compressTimeSec float64
		if s.config.CompressionThroughputMBps > 0 {
			compressTimeSec = sizeMB / s.config.CompressionThroughputMBps
		}
		// After compression, the actual data written is reduced by compression factor
		outputSizeMB := sizeMB * s.config.CompressionFactor
		ioTimeSec := outputSizeMB / s.config.IOThroughputMBps
		seekTimeSec := s.config.IOLatencyMs / 1000.0
		flushDuration := compressTimeSec + ioTimeSec + seekTimeSec

		// Flush can only start when disk is free
		flushStartTime := max(s.virtualTime, s.diskBusyUntil)
		flushCompleteTime := flushStartTime + flushDuration

		// Reserve disk bandwidth
		s.diskBusyUntil = flushCompleteTime

		// Track this write as in-progress for throughput calculation
		s.metrics.StartWrite(sizeMB, sizeMB, flushStartTime, flushCompleteTime, -1, 0) // Flush: memtable → L0

		// Schedule flush event with the SIZE that was frozen
		s.queue.Push(NewFlushEvent(flushCompleteTime, flushStartTime, sizeMB))

		// Track earliest flush completion time if we're stalled
		// This allows stalled writes to schedule retries at flush completion instead of every 1ms
		if s.numImmutableMemtables >= s.config.MaxWriteBufferNumber {
			// Find the earliest flush event (which might be the one we just scheduled, or an earlier one)
			earliestFlush := s.queue.FindNextFlushEvent()
			if earliestFlush != nil {
				s.nextFlushCompletionTime = earliestFlush.Timestamp()
			}
		}
	}

	// Writes are now scheduled continuously by ScheduleWriteEvent, independent of
	// whether individual writes succeed or are stalled. This ensures writes arrive
	// at the configured rate regardless of system state.
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

	// Update nextFlushCompletionTime for stalled writes
	// If still stalled, find the next flush completion time
	if s.numImmutableMemtables >= s.config.MaxWriteBufferNumber {
		// Still stalled - find next flush completion time
		nextFlush := s.queue.FindNextFlushEvent()
		if nextFlush != nil {
			s.nextFlushCompletionTime = nextFlush.Timestamp()
		} else {
			// No more flushes scheduled - fallback to 1ms retries
			s.nextFlushCompletionTime = 0
		}
	} else {
		// Stall cleared - no need to track flush completion time
		s.nextFlushCompletionTime = 0
	}

	// Compactions are handled by periodic CompactionCheckEvent, not triggered by flushes
	// This is acceptable - RocksDB also uses background threads that wake up periodically
}

// processWALWrite handles WAL write completion
func (s *Simulator) processWALWrite(event *WALWriteEvent) {
	// Note: WAL bytes and activity tracking are done in processWrite() before scheduling this event
	// No bandwidth refund needed with busy-until model
}

// processScheduleRead processes read batch scheduling and reserves disk bandwidth
func (s *Simulator) processScheduleRead(event *ScheduleReadEvent) {
	// Check if reads are enabled
	if s.config.ReadWorkload == nil {
		return // Reads disabled
	}

	// Read batch interval: process reads in 1 second batches (like compaction checks)
	readBatchIntervalSec := 1.0

	// Calculate total requests in this batch period
	totalRequestsPerSec := s.config.ReadWorkload.RequestsPerSec
	if totalRequestsPerSec <= 0 {
		// No reads to schedule - but still schedule next check
		s.scheduleNextScheduleRead(s.virtualTime + readBatchIntervalSec)
		return
	}

	// Total requests in this batch interval
	totalRequests := int(totalRequestsPerSec * readBatchIntervalSec)
	if totalRequests == 0 {
		// Too few requests to schedule - skip this batch
		s.scheduleNextScheduleRead(s.virtualTime + readBatchIntervalSec)
		return
	}

	// Break down requests by type
	cacheHits := int(float64(totalRequests) * s.config.ReadWorkload.CacheHitRate)
	bloomNegatives := int(float64(totalRequests) * s.config.ReadWorkload.BloomNegativeRate)
	scans := int(float64(totalRequests) * s.config.ReadWorkload.ScanRate)
	pointLookups := totalRequests - cacheHits - bloomNegatives - scans

	// Calculate disk bandwidth needed for this batch
	// Cache hits and bloom negatives don't use disk I/O
	// Point lookups read: blockSize * readAmp bytes per request
	// Scans read: avgScanSizeKB bytes per request

	// Get read amplification from metrics (calculated from LSM structure)
	readAmp := s.metrics.ReadAmplification
	if readAmp < 1.0 {
		readAmp = 1.0 // At minimum, one file must be read
	}

	blockSizeMB := float64(s.config.BlockSizeKB) / 1024.0
	scanSizeMB := s.config.ReadWorkload.AvgScanSizeKB / 1024.0

	pointLookupMB := float64(pointLookups) * blockSizeMB * readAmp
	scanMB := float64(scans) * scanSizeMB
	totalReadMB := pointLookupMB + scanMB

	if totalReadMB <= 0 {
		// No disk I/O needed for this batch (all cache hits/bloom negatives)
		s.scheduleNextScheduleRead(s.virtualTime + readBatchIntervalSec)
		return
	}

	// Calculate duration based on disk I/O
	// Duration = data_size / throughput + latency
	ioTimeSec := totalReadMB / s.config.IOThroughputMBps
	latencySec := s.config.IOLatencyMs / 1000.0
	readDuration := ioTimeSec + latencySec

	// Read batch can only start when disk is free
	readStartTime := max(s.virtualTime, s.diskBusyUntil)
	readCompleteTime := readStartTime + readDuration

	// Reserve disk bandwidth
	s.diskBusyUntil = readCompleteTime

	// Schedule read batch completion event
	readEvent := NewReadBatchEvent(readCompleteTime, readStartTime, totalRequests, pointLookups, scans, cacheHits, bloomNegatives)
	s.queue.Push(readEvent)

	// Schedule next ScheduleReadEvent
	s.scheduleNextScheduleRead(s.virtualTime + readBatchIntervalSec)
}

// processReadBatch handles read batch completion
func (s *Simulator) processReadBatch(event *ReadBatchEvent) {
	// Note: Read metrics are tracked separately by the metrics system
	// No bandwidth refund needed with busy-until model
}

// scheduleNextScheduleRead schedules the next ScheduleReadEvent
func (s *Simulator) scheduleNextScheduleRead(nextTime float64) {
	// Only schedule if reads are enabled (ReadWorkload != nil and RequestsPerSec > 0)
	if s.config.ReadWorkload == nil || s.config.ReadWorkload.RequestsPerSec <= 0 {
		return
	}
	s.queue.Push(NewScheduleReadEvent(nextTime))
}

// processCompaction processes a compaction event
func (s *Simulator) processCompaction(event *CompactionEvent) {
	compactionID := event.CompactionID()
	fromLevel := event.FromLevel()

	// Compactor handles activeCompactions tracking (cleared in ExecuteCompaction)

	// Retrieve the compaction job using compaction ID
	job, ok := s.pendingCompactions[compactionID]
	if !ok {
		fmt.Printf("[ERROR] No pending compaction job for ID %d (L%d→L%d)\n", compactionID, fromLevel, event.ToLevel())
		return
	}
	delete(s.pendingCompactions, compactionID)

	// Remove from activeCompactionInfos
	var newInfos []*ActiveCompactionInfo
	if len(s.activeCompactionInfos) > 0 {
		newInfos = make([]*ActiveCompactionInfo, 0, len(s.activeCompactionInfos)-1)
		for _, info := range s.activeCompactionInfos {
			if info.FromLevel != fromLevel || info.ToLevel != job.ToLevel {
				newInfos = append(newInfos, info)
			}
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

	// Calculate input size for logging
	var totalInputMB float64
	for _, f := range job.SourceFiles {
		totalInputMB += f.SizeMB
	}
	for _, f := range job.TargetFiles {
		totalInputMB += f.SizeMB
	}

	// Log compaction start
	compactionType := fmt.Sprintf("L%d→L%d", fromLevel, job.ToLevel)
	if job.IsIntraL0 {
		compactionType = "L0→L0"
	}
	s.logEvent("[COMPACTION START] %s: %d src files (%.1f MB) + %d tgt files (%.1f MB) = %.1f MB input",
		compactionType,
		len(job.SourceFiles), sourceSize,
		len(job.TargetFiles), totalInputMB-sourceSize,
		totalInputMB)

	// Track start time for duration calculation
	compactionStartTime := event.StartTime()

	// Execute the compaction using the compactor interface
	inputSize, outputSize, outputFileCount := s.compactor.ExecuteCompaction(job, s.lsm, s.config, s.virtualTime)

	if inputSize == 0 {
		return
	}

	// Update LSM total size (critical for FIFO compaction which manipulates files directly)
	// For leveled/universal, this is redundant with lsm.CompactLevel(), but harmless
	s.lsm.TotalSizeMB = s.lsm.TotalSizeMB - inputSize + outputSize

	// Calculate compaction duration and throughput
	compactionDuration := event.Timestamp() - compactionStartTime
	var compactionThroughput float64
	if compactionDuration > 0 {
		compactionThroughput = inputSize / compactionDuration
	}

	// Detect trivial move: no overlapping files in target level = metadata-only operation
	// RocksDB optimization: just updates file metadata (level pointer), no disk writes
	isTrivialMove := len(job.TargetFiles) == 0 && !job.IsIntraL0 && inputSize == outputSize

	// Log compaction completion with duration and throughput
	trivialMoveTag := ""
	if isTrivialMove {
		trivialMoveTag = " [trivial move]"
	}

	if compactionDuration < 0.01 {
		s.logEvent("[COMPACTION END] %s: %.1f MB output in <0.01s (%.1f MB/s throughput)%s",
			compactionType, outputSize, compactionThroughput, trivialMoveTag)
	} else {
		s.logEvent("[COMPACTION END] %s: %.1f MB output in %.2fs (%.1f MB/s throughput)%s",
			compactionType, outputSize, compactionDuration, compactionThroughput, trivialMoveTag)
	}

	// Update metrics with last compaction performance
	s.metrics.LastCompactionDurationSec = compactionDuration
	s.metrics.LastCompactionThroughputMBps = compactionThroughput

	// Move from in-progress to completed
	s.metrics.CompleteWrite(event.Timestamp(), fromLevel)
	inputFileCount := len(job.SourceFiles) + len(job.TargetFiles)
	s.metrics.RecordCompaction(inputSize, outputSize, event.StartTime(), event.Timestamp(), fromLevel, inputFileCount, outputFileCount, isTrivialMove)

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
	if len(s.pendingCompactions) >= s.config.MaxBackgroundJobs {
		return false
	}

	// Delegate compaction scheduling logic to the compactor
	// Compactor internally tracks active compactions and picks the best compaction
	job := s.compactor.PickCompaction(s.lsm, s.config)
	if job == nil {
		return false // No compaction needed
	}

	// Check if we've hit max parallel compactions
	// For now, we approximate by checking if we have too many pending compactions
	// TODO: Compactor should track this internally and return nil when at capacity
	activeCount := len(s.pendingCompactions)
	if activeCount >= s.config.MaxBackgroundJobs {
		// Can't schedule more - but compactor should have prevented this
		// If we get here, there's a bug: compactor returned a job when at capacity
		fmt.Printf("[WARNING] PickCompaction returned job but at max capacity (%d/%d)\n", activeCount, s.config.MaxBackgroundJobs)
		return false
	}

	fmt.Printf("[SCHEDULE] t=%.1f: L%d→L%d: scheduling compaction with %d source files, %d target files\n",
		s.virtualTime, job.FromLevel, job.ToLevel, len(job.SourceFiles), len(job.TargetFiles))

	// Check if subcompactions should be formed and split the job if needed
	//
	// RocksDB Reference: CompactionJob::Prepare() and GenSubcompactionBoundaries()
	// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_job.cc#L256-L280
	//
	// RocksDB C++ (lines 277-280):
	//
	//	if (!known_single_subcompact.has_value() && c->ShouldFormSubcompactions()) {
	//	  StopWatch sw(db_options_.clock, stats_, SUBCOMPACTION_SETUP_TIME);
	//	  GenSubcompactionBoundaries();
	//	}
	//
	// FIDELITY: ✓ Matches RocksDB's subcompaction splitting timing
	// Subcompactions are split at scheduling time, before duration calculation
	if ShouldFormSubcompactions(job, s.config, s.config.CompactionStyle) {
		// Get RNG from compactor (both leveled and universal have it)
		var rng *rand.Rand
		switch c := s.compactor.(type) {
		case *LeveledCompactor:
			rng = c.rng
		case *UniversalCompactor:
			rng = c.rng
		default:
			panic(fmt.Sprintf("unknown compactor type: %T", s.compactor))
		}

		// Split into subcompactions
		subcompactions := splitIntoSubcompactions(job, s.config, rng)
		if len(subcompactions) > 0 {
			job.Subcompactions = subcompactions
			fmt.Printf("[SCHEDULE] Split compaction into %d subcompactions\n", len(subcompactions))
		}
	}

	// Calculate input and output sizes
	// If subcompactions exist, calculate based on subcompactions (they split the work)
	var inputSize float64
	var outputSize float64
	var compactionDuration float64

	if len(job.Subcompactions) > 0 {
		// Subcompactions: calculate duration as max(subcompaction durations)
		//
		// RocksDB Reference: CompactionJob::RunSubcompactions()
		// Subcompactions run in parallel, so total duration = max(subcompaction durations)
		// GitHub: https://github.com/facebook/rocksdb/blob/main/db/compaction/compaction_job.cc#L710-L735
		//
		// FIDELITY: ✓ Matches RocksDB's parallel execution model
		// - Subcompactions execute in parallel threads
		// - Total duration = max(subcompaction durations) + small overhead
		maxSubcompactionDuration := 0.0
		for _, subcompaction := range job.Subcompactions {
			// Calculate input size for this subcompaction
			var subInputSize float64
			for _, f := range subcompaction.SourceFiles {
				subInputSize += f.SizeMB
			}
			for _, f := range subcompaction.TargetFiles {
				subInputSize += f.SizeMB
			}

			// Apply reduction factors (deduplication + compression)
			var deduplicationFactor float64
			if job.FromLevel == 0 && job.ToLevel == 1 {
				deduplicationFactor = s.config.DeduplicationFactor
			} else {
				deduplicationFactor = 0.99 // Minimal dedup for deeper levels
			}
			subOutputSize := subInputSize * deduplicationFactor * s.config.CompressionFactor

			// Calculate duration for this subcompaction
			// ADDITIVE MODEL: read I/O + decompress + compress + write I/O (sequential operations)
			readIOTimeSec := subInputSize / s.config.IOThroughputMBps
			var decompressTimeSec float64
			if s.config.DecompressionThroughputMBps > 0 {
				decompressTimeSec = subInputSize / s.config.DecompressionThroughputMBps
			}
			var compressTimeSec float64
			if s.config.CompressionThroughputMBps > 0 {
				compressTimeSec = subOutputSize / s.config.CompressionThroughputMBps
			}
			writeIOTimeSec := subOutputSize / s.config.IOThroughputMBps
			subSeekTimeSec := s.config.IOLatencyMs / 1000.0

			subDuration := readIOTimeSec + decompressTimeSec + compressTimeSec + writeIOTimeSec + subSeekTimeSec

			if subDuration > maxSubcompactionDuration {
				maxSubcompactionDuration = subDuration
			}

			// Accumulate total sizes
			inputSize += subInputSize
			outputSize += subOutputSize
		}

		// Total duration = max(subcompaction durations) + small overhead
		// Overhead accounts for synchronization, thread coordination, etc.
		const subcompactionOverhead = 0.01 // 10ms overhead
		compactionDuration = maxSubcompactionDuration + subcompactionOverhead
	} else {
		// Single compaction (no subcompactions)
		for _, f := range job.SourceFiles {
			inputSize += f.SizeMB
		}
		for _, f := range job.TargetFiles {
			inputSize += f.SizeMB
		}

		// Apply reduction factors (deduplication + compression)
		var deduplicationFactor float64
		if job.FromLevel == 0 && job.ToLevel == 1 {
			deduplicationFactor = s.config.DeduplicationFactor
		} else {
			deduplicationFactor = 0.99 // Minimal dedup for deeper levels
		}
		outputSize = inputSize * deduplicationFactor * s.config.CompressionFactor

		// Calculate compaction duration using ADDITIVE MODEL
		// Compaction process: read input → decompress → merge → compress → write output
		// All operations are sequential (cannot compress before decompressing input)
		readIOTimeSec := inputSize / s.config.IOThroughputMBps
		var decompressTimeSec float64
		if s.config.DecompressionThroughputMBps > 0 {
			decompressTimeSec = inputSize / s.config.DecompressionThroughputMBps
		}
		var compressTimeSec float64
		if s.config.CompressionThroughputMBps > 0 {
			compressTimeSec = outputSize / s.config.CompressionThroughputMBps
		}
		writeIOTimeSec := outputSize / s.config.IOThroughputMBps
		seekTimeSec := s.config.IOLatencyMs / 1000.0

		compactionDuration = readIOTimeSec + decompressTimeSec + compressTimeSec + writeIOTimeSec + seekTimeSec
	}

	// Compaction can only start when disk is free
	compactionStartTime := max(s.virtualTime, s.diskBusyUntil)
	compactionCompleteTime := compactionStartTime + compactionDuration

	// Reserve disk bandwidth
	s.diskBusyUntil = compactionCompleteTime

	// Compactor handles activeCompactions tracking (marked in PickCompaction)

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

	// Assign unique compaction ID
	compactionID := s.nextCompactionID
	s.nextCompactionID++
	job.ID = compactionID

	// Store the job so we can execute it when the event fires (keyed by compaction ID, not fromLevel)
	s.pendingCompactions[compactionID] = job

	// Track this write as in-progress for throughput calculation
	s.metrics.StartWrite(inputSize, outputSize, compactionStartTime, compactionCompleteTime, job.FromLevel, job.ToLevel)

	// Schedule compaction event (with subcompaction count if applicable)
	var compactionEvent *CompactionEvent
	if len(job.Subcompactions) > 0 {
		compactionEvent = NewCompactionEventWithSubcompactions(compactionCompleteTime, compactionStartTime, compactionID, job.FromLevel, job.ToLevel, inputSize, outputSize, len(job.Subcompactions))
	} else {
		compactionEvent = NewCompactionEvent(compactionCompleteTime, compactionStartTime, compactionID, job.FromLevel, job.ToLevel, inputSize, outputSize)
	}
	s.queue.Push(compactionEvent)

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
	for len(s.pendingCompactions) < s.config.MaxBackgroundJobs {
		scheduled := s.tryScheduleCompaction()
		if !scheduled {
			break // No more levels need compaction
		}
	}

	// Schedule next compaction check (every 1 virtual second, simulating background thread wake-ups)
	// CRITICAL: Always schedule from current virtualTime, NEVER from event.Timestamp()
	// Discrete event simulators should NEVER schedule events in the past
	// If event was processed late, schedule next event from NOW, not from event's timestamp
	checkInterval := 1.0
	nextCheckTime := s.virtualTime + checkInterval
	s.queue.Push(NewCompactionCheckEvent(nextCheckTime))
}

// processScheduleWrite processes a ScheduleWriteEvent
// This continuously schedules new writes at the configured rate, independent of
// whether writes are being stalled or not. This separation allows for flexible
// write arrival patterns (e.g., different distributions in the future).
func (s *Simulator) processScheduleWrite(event *ScheduleWriteEvent) {
	// Update traffic distribution with current virtual time (for advanced models)
	if advDist, ok := s.trafficDistribution.(*AdvancedTrafficDistribution); ok {
		advDist.UpdateTime(s.virtualTime)
	}

	// Check if traffic distribution indicates we should schedule writes
	writeSizeMB := s.trafficDistribution.NextWriteSizeMB()
	intervalSeconds := s.trafficDistribution.NextIntervalSeconds()

	if writeSizeMB <= 0 || intervalSeconds <= 0 {
		// No writes to schedule
		return
	}

	// Schedule the write event at current virtualTime (NOW)
	// CRITICAL: Always schedule from current virtualTime, NEVER from event.Timestamp()
	// Discrete event simulators should NEVER schedule events in the past
	// If event was processed late, schedule write from NOW, not from event's timestamp
	writeTime := s.virtualTime
	s.queue.Push(NewWriteEvent(writeTime, writeSizeMB))

	// Schedule the next ScheduleWriteEvent
	// CRITICAL: Always schedule from current virtualTime, NEVER from event.Timestamp()
	// This ensures self-perpetuating events are never scheduled in the past
	nextSchedulerTime := s.virtualTime + intervalSeconds
	s.scheduleNextScheduleWrite(nextSchedulerTime)
}

// scheduleNextScheduleWrite schedules the next ScheduleWriteEvent
func (s *Simulator) scheduleNextScheduleWrite(currentTime float64) {
	// Update traffic distribution with current virtual time (for advanced models)
	// Use s.virtualTime (actual current time) not currentTime parameter (which might be future time)
	if advDist, ok := s.trafficDistribution.(*AdvancedTrafficDistribution); ok {
		advDist.UpdateTime(s.virtualTime)
	}

	// Check if traffic distribution indicates we should schedule writes
	intervalSeconds := s.trafficDistribution.NextIntervalSeconds()
	if intervalSeconds <= 0 {
		return
	}
	nextSchedulerTime := currentTime + intervalSeconds
	s.queue.Push(NewScheduleWriteEvent(nextSchedulerTime))
}

// scheduleNextCompactionCheck schedules the next compaction check
// This simulates RocksDB's background compaction threads that periodically check for work
func (s *Simulator) scheduleNextCompactionCheck(currentTime float64) {
	// Check every 1 second of virtual time (simulating background thread wake-ups)
	checkInterval := 1.0
	nextCheckTime := currentTime + checkInterval
	s.queue.Push(NewCompactionCheckEvent(nextCheckTime))
}

// countStalledWrites counts the number of WriteEvents in the queue
// This provides an accurate count of stalled writes (excluding compaction events)
func (s *Simulator) countStalledWrites() int {
	if s.stallStartTime == 0 {
		return 0
	}
	// Count only WriteEvents in queue (excludes compaction/flush events)
	return s.queue.CountWriteEvents()
}

// ActiveCompactions returns the count of scheduled compactions (pending execution)
// These compactions are scheduled and waiting for their turn to execute (up to maxBackgroundJobs)
// With token bucket model, multiple compactions can execute in parallel up to disk bandwidth limit
func (s *Simulator) ActiveCompactions() int {
	return len(s.pendingCompactions)
}

// logEvent sends a log message to both stdout and the UI (if callback is set)
func (s *Simulator) logEvent(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	if s.LogEvent != nil {
		s.LogEvent(msg)
	}
}

// IsWriteStalled returns true if writes are currently stalled due to memtable backpressure
func (s *Simulator) IsWriteStalled() bool {
	return s.stallStartTime > 0
}

// ScheduleWrite schedules a write event at the specified virtual time
func (s *Simulator) ScheduleWrite(sizeMB float64, timestamp float64) {
	writeEvent := &WriteEvent{
		timestamp: timestamp,
		sizeMB:    sizeMB,
	}
	s.queue.Push(writeEvent)
}

// StepUntil advances the simulation until the specified target virtual time is reached
func (s *Simulator) StepUntil(targetTime float64) float64 {
	for s.virtualTime < targetTime && !s.queue.IsEmpty() {
		if s.metrics.IsOOMKilled {
			break
		}
		s.Step()
	}
	return s.virtualTime
}

// StepByDelta advances the simulation by the specified time delta (in seconds)
func (s *Simulator) StepByDelta(deltaSeconds float64) float64 {
	targetTime := s.virtualTime + deltaSeconds
	return s.StepUntil(targetTime)
}

// Helper functions
