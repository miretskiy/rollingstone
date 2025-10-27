# RollingStone Development Guide

## Simulation Fidelity

RollingStone aims to be a high-fidelity simulator of RocksDB's LSM tree behavior, based on the [official RocksDB documentation](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction).

### What We Model

#### LSM Tree Structure
- **Memtable**: In-memory write buffer (64-256 MB typical)
- **Immutable Memtables**: Queue of frozen memtables waiting for flush
- **L0 (Tiered)**: Overlapping SST files from memtable flushes
- **L1-L6 (Leveled)**: Non-overlapping sorted runs, exponentially growing

#### Compaction Behavior

**L0 → L1 Compaction:**
- Triggered when L0 file count ≥ `level0_file_num_compaction_trigger` (default: 4)
- Usually picks all L0 files (they overlap)
- Merges with overlapping L1 files
- Applies reduction factor (0.9 for L0→L1, simulating deduplication)
- **Not parallelized by default** (RocksDB limitation)

**L1+ → L1+ Compaction:**
- Scored by `size / target_size` (or file_count / trigger for L0)
- Highest-scoring level compacts first
- Picks subset of files using statistical distributions:
  - Source files: Uniform or Exponential distribution
  - Overlapping files in next level: Exponential distribution
- Splits output into multiple files based on `target_file_size_base * multiplier^(level-1)`
- **Can run in parallel** (up to `max_background_jobs`, default: 2)

**Dynamic Thresholds:**
- If target level is empty: require 2.0x over target before compacting
- If target level has 1-2 files: require 1.5x over target
- Otherwise: require 1.0x over target (compact when over target)

This prevents premature compaction into empty levels and allows levels to naturally accumulate files.

#### I/O Modeling

**Disk as Shared Resource:**
- Single `diskBusyUntil` token tracks when disk is free
- All flushes and compactions consume from same bandwidth pool
- Operation duration = `(inputSize + outputSize) / ioThroughputMBps`

**I/O Profiles:**
- EBS gp3: 500 MB/s, 3ms latency
- NVMe: 3000 MB/s, 0.1ms latency
- HDD: 150 MB/s, 10ms latency

**Write Stalls:**
- When `numImmutableMemtables >= maxWriteBufferNumber`, incoming writes stall
- WriteEvent rescheduled with 100ms delay
- Simulates RocksDB's backpressure mechanism

#### Background Compaction Threads

**CompactionCheckEvent** (fires every 1 virtual second):
- Simulates RocksDB's background compaction threads
- Scores all levels
- Schedules compactions up to `maxBackgroundJobs` slots
- Decoupled from flush events (compactions happen independently)

### What We Simplify

#### No Key Tracking
- Don't track actual keys or ranges
- Use statistical distributions for file selection and overlap
- Much faster than tracking every key

#### No Intra-L0 Compaction (Yet)
- RocksDB can compact L0 files with each other before L1
- Not currently implemented (TODO)

#### No Subcompactions (Yet)
- RocksDB can parallelize L0→L1 using subcompactions
- Currently, L0→L1 uses 1 job slot (faithful to default RocksDB)

#### Simplified Read Simulation
- Read amplification = `1 + numImmutableMemtables + L0_file_count + num_levels_with_data`
- No actual read workload or bloom filter modeling (yet)

### Metrics Tracked

#### Write Amplification
- `WA = TotalBytesWrittenToDisk / TotalBytesWrittenByUser`
- Includes flushes and compactions

#### Read Amplification
- `RA = 1 + numImmutableMemtables + L0_fileCount + (num_non_empty_levels)`
- Represents worst-case file lookups for a point query

#### Space Amplification
- `SA = TotalSizeOnDisk / LogicalDataSize`
- Tracks overhead from uncompacted data and obsolete keys

#### Throughput (Instantaneous)
- **Window**: 100ms around current time (`[now-0.05s, now+0.05s]`)
- **Calculation**: Sum bandwidth of all active I/O operations
- **Capping**: Scaled proportionally if sum exceeds physical disk limit

## UI Design

### Component Structure

```
App
├── Header (RollingStone title, connection status)
├── SimulationControls
│   ├── Play/Pause/Reset buttons
│   ├── LSM Configuration (collapsible)
│   ├── Workload & Traffic (collapsible)
│   ├── I/O Performance (collapsible)
│   └── Advanced LSM Tuning (collapsible)
├── MetricsDashboard
│   ├── Key metrics (4-card grid)
│   ├── Write throughput (numeric display)
│   └── Active I/O operations (compaction details)
└── LSMTreeVisualization
    ├── Memtable (green bar)
    └── Levels (L0-L6, size bars, compaction indicators)
```

### Collapsible Sections

Each config section shows a **summary line** when collapsed:
- "LSM Configuration: 7 levels, 64 MB memtable, L0 trigger: 4"
- "Workload: 10.0 MB/s write rate"
- "I/O Performance: EBS gp3 (500 MB/s)"

Click to expand/collapse. Keeps UI manageable.

### Static vs Dynamic Parameters

**Static (disabled while running):**
- LSM structure, compaction settings, I/O profiles
- Require simulation reset to apply
- Shows "Reset required" badge when changed

**Dynamic (adjustable live):**
- `writeRateMBps` slider (0-500 MB/s)
- Changes take effect immediately (next WriteEvent)

### Presets

**LSM Presets:**
- Small (3 levels) - for debugging
- Default (7 levels) - standard RocksDB
- Large (9 levels) - very large databases

**I/O Presets:**
- EBS gp3, EBS io2, NVMe, HDD, Custom
- Sets `ioThroughputMBps` and `ioLatencyMs`

### Visual Indicators

**Compaction Indicators:**
- Yellow pulsing dot + border on level card
- Shows input→output sizes: "Compacting: 500.0 MB → 495.0 MB"
- Multiple compactions shown as comma-separated

**Size Bars:**
- Logarithmic scale (handles wide size ranges)
- Color gradient: blue (small) → purple (large)

**Active I/O:**
- Count: "3 active writes"
- Details: "Flush → L0: 64.0 MB", "L1→L2: 500.0 MB → 495.0 MB"

## Code Structure

### Backend (`simulator/`)

**simulator.go:**
- `Simulator` struct: Holds all simulation state
- `Step()`: Process events until `targetTime` reached
- `processEvent()`: Dispatch to specific event handlers
- `processWrite()`, `processFlush()`, `processCompaction()`, `processCompactionCheck()`
- `tryScheduleCompaction()`: Score levels, pick highest-priority compaction

**lsm.go:**
- `LSMTree` struct: MemTable + Levels
- `FlushMemtable()`: Create L0 file from frozen memtable
- `CreateSSTFile()`: Generate SST file with ID
- `calculateCompactionScore()`: Score = size/target (or fileCount/trigger for L0)
- `pickLevelToCompact()`: Find highest-scoring level above threshold
- `State()`: Serialize LSM tree for UI (limit 20 files/level)

**compactor.go:**
- `Compactor` interface: `NeedsCompaction()`, `PickCompaction()`, `ExecuteCompaction()`
- `LeveledCompactor`: Statistical file selection (no key tracking)
- `PickCompaction()`: Select source files + overlapping target files using distributions
- `ExecuteCompaction()`: Merge files, apply reduction, split into target-sized files

**metrics.go:**
- `Metrics` struct: Tracks amplifications, throughput, latencies
- `RecordWrite()`, `RecordFlush()`, `RecordCompaction()`
- `StartWrite()`, `CompleteWrite()`: Track in-progress I/O for throughput
- `calculateThroughput()`: Instantaneous bandwidth calculation (100ms window)
- `CapThroughput()`: Enforce physical disk limits

**events.go:**
- `Event` interface: `Timestamp()`, `Type()`
- `WriteEvent`, `FlushEvent`, `CompactionEvent`, `CompactionCheckEvent`
- Each event stores start time for duration tracking

**event_queue.go:**
- Min-heap priority queue (ordered by event timestamp)
- `Push()`, `Pop()`, `Peek()`, `Len()`

### Frontend (`web/src/`)

**store.ts:**
- Zustand store (global state)
- WebSocket connection management
- Message dispatcher (`handleMessage()`)
- State: `connectionStatus`, `isRunning`, `currentConfig`, `currentMetrics`, `currentState`, `metricsHistory`

**types.ts:**
- TypeScript interfaces for all messages and data structures
- `SimulationConfig`, `SimulationMetrics`, `SimulationState`, `WSMessage`

**components/:**
- Each component is self-contained
- Uses Tailwind for styling, Lucide for icons
- Radix UI for accessible components (Slider, Collapsible)

## Development Workflow

### Making Changes

**Backend:**
1. Edit `simulator/*.go`
2. Run: `go build -o /tmp/rollingstone cmd/server/main.go`
3. Test: `/tmp/rollingstone` and open `http://localhost:8080`

**Frontend:**
1. Edit `web/src/**/*.tsx`
2. Vite auto-reloads (if running `npm run dev`)
3. Or rebuild: `cd web && npm run build` (output to `web/dist/`)

### Testing Configurations

**Extreme Workload (Stress Test):**
- Write rate: 250-500 MB/s
- I/O: 500 MB/s (EBS)
- Compaction parallelism: 4-6 jobs
- Expect: Write stalls, L1+ compactions, high write amplification

**Gentle Workload (Steady State):**
- Write rate: 10-50 MB/s
- I/O: 500 MB/s
- Compaction parallelism: 2 jobs
- Expect: Stable L0, periodic compactions, moderate amplification

### Debugging

**Backend Logs:**
- `[SCORE]`: Compaction scoring output (every check)
- `[SCHEDULE]`: When compactions are scheduled
- `[EXECUTE]`: Compaction execution details
- View: `tail -f /tmp/server.log`

**Frontend:**
- Metrics history: `metricsHistory` in Zustand store
- Connection issues: Check `connectionStatus` state
- Message flow: Uncomment `console.log` in `store.ts` (careful: causes memory bloat)

### Performance Tuning

**Browser Memory Issues:**
- Reduce UI update frequency (currently 500ms)
- Limit metrics history size (currently 500 entries)
- Disable charts (Recharts has memory leaks with frequent updates)
- Cap files per level (currently 20)

**Simulation Speed:**
- Increase step size (currently 0.1s per step)
- Reduce UI update frequency
- Profile with Go pprof if needed

## Future Work

### High Priority
- [ ] Add tooltips to all configuration options
- [ ] Backend auto-start/stop frontend (unified binary)
- [ ] Implement intra-L0 compactions
- [ ] Add read workload simulation

### Medium Priority
- [ ] Subcompactions for L0→L1 parallelization
- [ ] `level_compaction_dynamic_level_bytes=true` mode
- [ ] Bloom filter modeling (reduce read amplification)
- [ ] Chart improvements (fix memory issues, re-enable)

### Low Priority
- [ ] CLI tool for batch simulations
- [ ] Export metrics to CSV/JSON
- [ ] Comparison mode (run multiple configs side-by-side)
- [ ] Animation speed control (faster/slower simulation playback)

## Contributing

When adding features:
1. **Maintain fidelity**: Check RocksDB docs for correct behavior
2. **Add debug logging**: Use `fmt.Printf` with prefixes like `[FEATURE]`
3. **Test extreme configs**: High write rates, many jobs, stress test
4. **Update docs**: Keep README and this file in sync
5. **Memory conscious**: Profile browser memory if adding UI updates

## License

MIT

