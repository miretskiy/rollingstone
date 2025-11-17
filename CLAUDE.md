# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

RollingStone is a **discrete event simulator (DES)** for RocksDB LSM trees with a Go backend and React frontend. It simulates RocksDB's LSM tree behavior to analyze performance, visualize structure, and optimize configurations without running full benchmarks.

**Key Architecture:**
- Go backend (`simulator/`) - Pure discrete event simulator with no concurrency primitives
- WebSocket server (`cmd/server/main.go`) - Handles bidirectional communication and serves embedded static files
- React frontend (`web/`) - Real-time visualization with Zustand state management

## High-Fidelity "Flight Simulator" Design

RollingStone is not a toy simulator - it's a **high-fidelity "flight simulator"** for RocksDB LSM trees designed for production capacity planning and performance analysis.

**Fidelity Status:**
- **71% verified fidelity** (5/7 major features verified against RocksDB source)
- See `FIDELITY_AUDIT_REPORT.md` for detailed feature-by-feature analysis
- Compaction scoring, file selection, and write stalls match RocksDB exactly
- Some features simplified (statistical overlap vs key-based) for performance

**RocksDB Source Reference:**
- Full RocksDB source code available at `~/src/rocksdb/` for verification
- Simulator code cross-references actual C++ implementation with file:line citations
- Critical logic is annotated with actual RocksDB C++ snippets for validation

**Design Philosophy:**
- **Verify against source** - All critical behavior cross-checked with RocksDB C++ code
- **Document deviations** - Every simplification is marked and justified
- **Maintain determinism** - Same config + same seed → same results (for testing)
- **Production-grade** - Used for real capacity planning, not just learning

## Fidelity Verification System

The simulator uses a strict fidelity annotation system to track how closely each component matches RocksDB's behavior. **See `simulator/lsm.go` for exemplar fidelity annotations.**

### Fidelity Marker System

Every significant simulation logic should include:

1. **RocksDB Reference Comment Block**
   ```go
   // FIDELITY: RocksDB Reference - Feature Name
   // https://github.com/facebook/rocksdb/blob/main/path/to/file.cc#L123-L456
   //
   // C++ snippet from RocksDB source:
   //
   //   ```cpp
   //   // Actual RocksDB code copied from source
   //   if (condition) {
   //     do_something();
   //   }
   //   ```
   ```

2. **Fidelity Marker**
   - `// FIDELITY: ✓ Exact match` - Simulator behavior matches RocksDB exactly
   - `// FIDELITY: ⚠️ SIMPLIFIED` - Intentional simplification with explanation
   - `// FIDELITY: ✗ NOT IMPLEMENTED` - Feature exists in RocksDB but not here

3. **Implementation Code**
   ```go
   // Your simulation code that implements (or approximates) the RocksDB behavior
   ```

### Example from lsm.go

```go
// FIDELITY: RocksDB Reference - Memtable Flush Triggers
// https://github.com/facebook/rocksdb/blob/main/db/db_impl/db_impl_write.cc#L1432-L1445
//
// C++ snippet from DBImpl::HandleWriteBufferManagerFlush():
//
//   ```cpp
//   if (cfd->imm()->NumNotFlushed() >= cfd->ioptions()->max_write_buffer_number) {
//     return true;  // Too many immutable memtables
//   }
//   ```
//
// FIDELITY: ✓ Size-based flush matches RocksDB's write_buffer_size check
func (t *LSMTree) NeedsFlush() bool {
    return t.MemtableCurrentSize >= t.MemtableMaxSize
}
```

### Critical Requirements

**⚠️ IMPORTANT: Comments Must Match Implementation**

The fidelity comments are NOT decorative - they are contracts:
- If you add a RocksDB C++ snippet, the Go code below MUST implement that behavior
- If implementation deviates, mark it `⚠️ SIMPLIFIED` and explain WHY
- If you can't implement it yet, mark it `✗ NOT IMPLEMENTED` with a TODO

**Verification Workflow:**

1. **Find RocksDB Source** - Locate relevant code in `~/src/rocksdb/`
   - Use GitHub search or `grep` in local repo
   - Common files: `db/version_set.cc`, `db/compaction/compaction_picker_level.cc`, `db/db_impl/db_impl_write.cc`

2. **Copy C++ Snippet** - Extract 5-15 lines showing the key logic
   - Include surrounding context if needed
   - Add file path and line numbers as GitHub URL

3. **Implement in Go** - Write simulation code that matches the C++ behavior
   - Use same variable names when possible
   - Preserve logic flow and conditions

4. **Mark Fidelity Level**
   - ✓ if exact match
   - ⚠️ if simplified (explain reason: performance, complexity, statistical model)
   - ✗ if not yet implemented

5. **Test Against RocksDB** - Validate behavior matches expectations
   - Run benchmarks if possible
   - Cross-check with `FIDELITY_AUDIT_REPORT.md`

**Files with Good Fidelity Annotations:**
- `simulator/lsm.go` - Exemplar, study this first
- `simulator/leveled_compaction.go` - Complex logic with multiple markers
- `simulator/simulator.go` - Event processing and write stalls
- `simulator/universal_compaction.go` - Universal compaction strategy

## Benchmarking & Performance Analysis

The `benchmarks/` directory contains empirical performance data from RocksDB to validate simulator models.

### Available Benchmarks

**Local RocksDB Benchmarks** (using db_bench):
```bash
# Quick benchmarks (1M keys, 5-10 min)
cd benchmarks && ./run_benchmarks_small.sh

# Full benchmarks (10M keys, 30-60 min)
cd benchmarks && ./run_benchmarks.sh

# Timeseries workload (Datadog-relevant, not yet run)
cd benchmarks && ./run_timeseries.sh
```

**Results Available:**
- `benchmarks/results/` - Full benchmark results (10M keys)
- `benchmarks/results_small/` - Quick benchmark results (1M keys)
- Tests: fillseq, readrandom, readwhilewriting, seekrandom
- Compression: none, snappy, zstd

### Key Analysis Documents

**PRIMARY ANALYSIS: `benchmarks/cpu_cost_analysis.md`**
- Synthesized from AWS published benchmarks
- **Key Finding**: I/O is the bottleneck, NOT CPU
- Write path: Compression CPU < 10% of total time
- Read path: Decompression is fast, cache misses are I/O-bound
- **Recommendation**: Don't add full CPU modeling yet (focus on I/O)

**Secondary Analysis: `benchmarks/benchmark_analysis.md`**
- Analysis of local Mac ARM64 benchmarks
- Compression actually improves throughput on Apple Silicon
- Point read performance: 7.4M ops/sec when cached

### Important Resources

**RocksDB Benchmarking Documentation:**
- [Benchmarking Tools](https://github.com/facebook/rocksdb/wiki/Benchmarking-tools) - db_bench usage and options
- [Performance Benchmarks](https://github.com/facebook/rocksdb/wiki/performance-benchmarks) - AWS published results (used in cpu_cost_analysis.md)
- Hardware: AWS m5d.2xlarge (8 CPU, 32GB RAM, NVMe SSD, 117K IOPS)

**Benchmark Status:**
- ✓ fillseq benchmarks complete
- ✓ read benchmarks complete
- ✓ CPU cost analysis synthesized from AWS data
- ⏳ Timeseries workload (Datadog-relevant) - **NOT YET RUN**

### When to Run Benchmarks

**Run local benchmarks when:**
- Validating I/O models (disk throughput, latency)
- Testing compression impact on write path
- Measuring read latencies and cache behavior
- Verifying simulator predictions against real RocksDB

**Use AWS published benchmarks for:**
- Production-grade hardware baselines
- CPU vs I/O bottleneck analysis
- Cross-platform comparison (x86 vs ARM)

## Essential Commands

### Building & Running

```bash
# Quick start (builds everything and starts server)
./start.sh
# Server runs at http://localhost:8080

# Manual build (frontend + backend)
./build.sh

# Build backend only
go build -o rollingstone ./cmd/server

# Build frontend only (for development)
cd web && npm install && npm run build
```

### Testing

```bash
# Run all tests
go test ./simulator/...

# Run specific test file
go test ./simulator/compactor_test.go

# Run specific test function
go test ./simulator -run TestLeveledCompactor

# Run with verbose output
go test -v ./simulator/...
```

### Benchmarking

```bash
# Quick RocksDB benchmarks (5-10 minutes, 1M keys)
cd benchmarks && ./run_benchmarks_small.sh

# Full RocksDB benchmarks (30-60 minutes, 10M keys)
cd benchmarks && ./run_benchmarks.sh

# Timeseries workload (Datadog-relevant)
cd benchmarks && ./run_timeseries.sh
```

### Development

```bash
# Run backend only (useful during development)
go run cmd/server/main.go

# Run frontend in dev mode with hot reload
cd web && npm run dev
# Vite dev server at http://localhost:3000 (proxies to :8080)

# Stop server gracefully
curl http://localhost:8080/quitquitquit

# View server logs
tail -f server.log
```

## Architecture & Design

### Discrete Event Simulation (Core Principle)

The simulator is **pull-based**, not push-based:
- No goroutines or concurrency primitives in `simulator/` package
- All state accessed single-threaded via `Step()` method
- Virtual clock drives execution (no real-time delays)
- UI controls pacing via `Step()` calls
- Event queue is a min-heap priority queue ordered by timestamp

**Event Types:**
- `WriteEvent` - Incoming writes (driven by configured rate)
- `FlushEvent` - Memtable → L0 flush (size/time threshold)
- `CompactionEvent` - Ln → Ln+1 compaction
- `CompactionCheckEvent` - Background thread checking for compaction needs (fires every 1 virtual second)

### Key Components

**simulator/simulator.go**
- Event loop, virtual clock, state machine
- `Step(targetTime)` - Process events until target time reached
- `processEvent()` - Dispatch to specific event handlers
- No concurrency - all state mutations are sequential

**simulator/lsm.go**
- LSM tree data structures (memtable, levels, files)
- `calculateCompactionScore()` - Implements RocksDB's scoring algorithm
- L0: `max(fileCount / trigger, totalSize / max_bytes_for_level_base)`
- L1+: `(totalSize - compactingSize) / targetSize`

**simulator/compactor.go**
- Compaction strategies (Leveled, Universal)
- `LeveledCompactor` - Statistical file selection without key tracking
- Uses exponential/uniform distributions for file overlap

**simulator/leveled_compaction.go**
- Leveled compaction logic
- L0→L1: Picks ALL L0 files (matches RocksDB)
- L1+: Uses `pickSourceCount()` and `pickOverlapCount()` for statistical selection
- Dynamic thresholds prevent premature compaction into empty levels

**simulator/universal_compaction.go**
- Universal compaction strategy
- Size-ratio triggered compactions
- All files in same level

**simulator/metrics.go**
- Performance tracking (amplifications, throughput)
- Write Amplification: `TotalBytesWrittenToDisk / TotalBytesWrittenByUser`
- Read Amplification: `1 + numImmutableMemtables + L0_fileCount + num_non_empty_levels`
- Space Amplification: `TotalSizeOnDisk / LogicalDataSize`
- Instantaneous throughput: 100ms window calculation with disk limit capping

**simulator/events.go**
- Event type definitions implementing `Event` interface
- Each event stores start time for duration tracking

**cmd/server/main.go**
- WebSocket server with gorilla/websocket
- Command dispatcher (start, pause, reset, config_update)
- UI update loop (500ms ticker)
- Embedded static files (go:embed)
- `safeConn` mutex wrapper prevents concurrent WebSocket writes

**web/src/store.ts**
- Zustand global state store
- WebSocket connection management
- Message dispatcher handling all server messages
- Metrics history capped at 500 entries

### Simulation Fidelity

RollingStone aims for high fidelity to RocksDB behavior:

**What We Model:**
- Tiered L0 (overlapping files) + Leveled L1-L6 (non-overlapping sorted runs)
- Compaction scoring exactly matching RocksDB algorithm
- Parallel compactions (up to `max_background_jobs`)
- Disk as shared resource with token bucket (`diskBusyUntil`)
- Write stalls when memtable queue backs up
- I/O profiles (EBS gp3, NVMe, HDD)
- Dynamic level thresholds (2.0x/1.5x/1.0x based on target level state)

**What We Simplify:**
- No key tracking - uses statistical distributions for file selection
- Simplified read simulation (no actual read workload yet)
- No bloom filter modeling
- Intra-L0 compaction uses simplified threshold (2 files vs RocksDB's 4)

**Known Discrepancies (see FIDELITY_AUDIT_REPORT.md):**
- Intra-L0 threshold: 2 files (simulator) vs 4 files (RocksDB)
- No compensated_file_size tracking (uses raw file size)
- Statistical overlap vs key-based overlap

### WebSocket Protocol

**Client → Server:**
- `{type: "start"}` - Start simulation
- `{type: "pause"}` - Pause simulation
- `{type: "reset"}` - Reset simulation
- `{type: "config_update", config: {...}}` - Update configuration

**Server → Client:**
- `{type: "status", running: bool, config: SimulationConfig}` - Status update
- `{type: "metrics", metrics: {...}}` - Metrics update (every 500ms)
- `{type: "state", state: {...}}` - LSM tree snapshot

**Configuration Parameters:**
- **Static** (require reset): `numLevels`, `memtableFlushSizeMB`, `l0CompactionTrigger`, `maxBytesForLevelBaseMB`, `levelMultiplier`, `targetFileSizeMB`, `maxBackgroundJobs`, `ioThroughputMBps`, `ioLatencyMs`
- **Dynamic** (live adjustment): `writeRateMBps`

### I/O Modeling

**Disk Contention:**
- Single `diskBusyUntil` token tracks when disk is free
- Operation start time = `max(virtualTime, diskBusyUntil)`
- Duration = `(inputSize + outputSize) / ioThroughputMBps`

**Write Stalls:**
- Triggered when `numImmutableMemtables >= maxWriteBufferNumber`
- WriteEvent rescheduled with 100ms delay
- Simulates RocksDB's backpressure mechanism

**I/O Profiles:**
- EBS gp3: 500 MB/s, 3ms latency
- NVMe: 3000 MB/s, 0.1ms latency
- HDD: 150 MB/s, 10ms latency

## Code Conventions

### Simulation Code
- All simulation logic must be single-threaded and deterministic
- Never add concurrency primitives (mutexes, channels, goroutines) to `simulator/` package
- Use `sim.LogEvent(msg string)` for debugging output (forwarded to UI)
- Prefix debug logs with `[CATEGORY]` for filtering (e.g., `[SCORE]`, `[SCHEDULE]`, `[EXECUTE]`)

### Compaction Strategies
- Implement `Compactor` interface for new strategies
- `NeedsCompaction()` - Check if any level needs compaction
- `PickCompaction()` - Select source and target files
- `ExecuteCompaction()` - Perform the compaction operation

### Adding Events
- Implement `Event` interface with `Timestamp()` and `Type()` methods
- Add handler in `simulator.go` `processEvent()` switch
- Schedule event via `sim.queue.Push(event)`

### Frontend Components
- Use Tailwind CSS for styling
- Use Lucide React for icons
- Use Radix UI for accessible components
- Keep components self-contained
- Store global state in `store.ts` (Zustand)

### Testing
- Write table-driven tests for compaction logic
- Use `testify/assert` for assertions
- Test extreme configurations (high write rates, many jobs)
- Verify determinism (same seed → same results)

## Important Implementation Details

### Compaction Scoring
The simulator implements RocksDB's exact scoring algorithm (verified in FIDELITY_AUDIT_REPORT.md):
- L0 uses dual scoring: `max(fileCount / trigger, totalSize / targetSize)`
- L1+ uses size ratio: `(totalSize - compactingSize) / targetSize`
- Dynamic thresholds prevent premature compaction into empty levels

### L0→L1 Compaction
Critical behavior that matches RocksDB:
- Picks ALL L0 files (no subset selection)
- Can exceed `max_compaction_bytes` (verified against RocksDB source)
- Applies reduction factor (default 0.9) for deduplication
- Not parallelized by default (RocksDB limitation)

### File Selection Algorithm
Uses statistical distributions to avoid key tracking:
- Source files: Uniform or Exponential distribution
- Overlapping files: Exponential distribution
- Configurable via `OverlapDistribution` parameter

### Throughput Calculation
Instantaneous bandwidth in 100ms window:
- Sum bandwidth of all active I/O operations in `[now-0.05s, now+0.05s]`
- Scale proportionally if sum exceeds physical disk limit
- Track in-progress I/O for accurate calculation

## Performance Considerations

### Browser Memory
- Limit to 20 files per level in UI (prevent DOM bloat)
- Throttle UI updates to 500ms (not 50ms)
- Cap metrics history at 500 entries
- Charts currently disabled (Recharts memory leak)

### Simulation Speed
- Event-driven: Processes millions of events/sec
- Pull-based: UI controls pacing (no runaway simulation)
- Deterministic: Same seed → same results

## Common Development Tasks

### Adding New Compaction Strategy
1. Create new file in `simulator/` (e.g., `tiered_compaction.go`)
2. Implement `Compactor` interface
3. Add to `NewSimulator()` switch in `simulator.go`
4. Add config option in `SimConfig`
5. Write tests in `*_test.go`

### Adding New Event Type
1. Define struct in `simulator/events.go`
2. Implement `Event` interface
3. Add case to `processEvent()` in `simulator.go`
4. Schedule event where appropriate
5. Test event ordering and timing

### Modifying UI
1. Edit component in `web/src/components/`
2. Update types in `web/src/types.ts` if needed
3. Rebuild: `cd web && npm run build`
4. Test with `./start.sh`

### Adding Configuration Parameter
1. Add field to `SimConfig` in `simulator/config.go`
2. Add validation in `config.Validate()`
3. Update UI in `web/src/components/SimulationControls.tsx`
4. Update types in `web/src/types.ts`
5. Test reset vs live update behavior

## Debugging

### Backend Logs
- Location: `server.log` in project root
- View live: `tail -f server.log`
- Key prefixes: `[SCORE]`, `[SCHEDULE]`, `[EXECUTE]`, `[FLUSH]`

### Frontend State
- Open React DevTools
- Inspect Zustand store state
- Check `metricsHistory` for historical data
- Monitor WebSocket messages in Network tab

### Simulation Issues
- Check event queue ordering (events must process in time order)
- Verify disk token (`diskBusyUntil`) prevents time-travel
- Ensure compaction scores are calculated correctly
- Verify write stall logic prevents OOM

## References

- [RocksDB Leveled Compaction](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction)
- [RocksDB Tuning Guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)
- [RocksDB Performance Benchmarks](https://github.com/facebook/rocksdb/wiki/performance-benchmarks)
- [Discrete Event Simulation](https://en.wikipedia.org/wiki/Discrete-event_simulation)

See also: `docs/ARCHITECTURE.md`, `docs/DEVELOPMENT.md`, `FIDELITY_AUDIT_REPORT.md`
