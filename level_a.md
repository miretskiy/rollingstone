# Level A/B Fidelity Implementation Roadmap

## Executive Summary

**Current State:** C+ (70-75% fidelity)
- ✅ Exceptional compaction logic (leveled, universal, dynamic level bytes)
- ✅ Write path fundamentals (memtable, flush, stalls)
- ✅ Metrics infrastructure (write amp, space amp, throughput)
- ❌ Missing: WAL, CPU modeling, read path, bloom filters, dynamic throttling

**Target State:**
- **Level B (90% fidelity):** 1-2 weeks - Production-ready for capacity planning
- **Level A (95% fidelity):** 2-3 weeks - High-confidence tuning decisions

**Timeline:**
- **Week 1:** Write path extensions (WAL, CPU, write controller, compression)
- **Week 2:** Read path (reads, bloom filters, block cache)
- **Week 3:** Validation & Level A features (TTL compaction, multi-thread CPU)

---

## Architecture Principles

### Library-First Design

**Core Principle:** Go simulation code must be usable as a standalone library.

```go
// Other agents should be able to do this:
import "github.com/yevgeniy-miretskiy/rollingstone/simulator"

config := simulator.DefaultConfig()
config.WriteRateMBps = 100.0
config.CompressionAlgorithm = "zstd"

sim, _ := simulator.NewSimulator(config)
sim.Run(60.0) // Run for 60 seconds

metrics := sim.Metrics()
fmt.Printf("Write Amp: %.2f\n", metrics.WriteAmplification)
```

**No UI Dependencies:**
- All simulation logic in `simulator/` package
- UI is just a visualization layer
- Config validation in Go, not JavaScript

**Config Management:**
- `SimConfig` struct is source of truth
- UI serializes/deserializes JSON representation
- Agents import Go structs directly

---

## Feature Implementation Details

---

## 1. WAL (Write-Ahead Log)

### Current Gap
- No WAL writes tracked
- Write amplification underestimated by 15-20%
- Cannot model WAL sync latency tuning

### Implementation

#### Go Library Structure

```go
// In simulator/events.go
type WALWriteEvent struct {
    BaseEvent
    sizeMB        float64
    syncLatencyMs float64
}

func NewWALWriteEvent(timestamp, sizeMB float64) *WALWriteEvent {
    return &WALWriteEvent{
        BaseEvent:     BaseEvent{timestamp: timestamp},
        sizeMB:        sizeMB,
        syncLatencyMs: 0, // Set by config
    }
}
```

#### Integration Point

Modify `simulator.go:processWriteEvent()`:

```go
func (s *Simulator) processWriteEvent(event *WriteEvent) {
    // 1. Write to WAL BEFORE memtable (durability)
    if s.config.EnableWAL {
        walDuration := s.writeToWAL(event.SizeMB)

        // WAL write blocks, reserves disk bandwidth
        s.diskBusyUntil = max(s.virtualTime, s.diskBusyUntil) + walDuration

        // Track WAL bytes
        s.metrics.WALBytesWritten += event.SizeMB
        s.metrics.TotalDiskWritten += event.SizeMB
    }

    // 2. Add to memtable (no I/O)
    s.lsm.AddWrite(event.SizeMB, s.virtualTime)

    // 3. Check for flush, stalls, etc. (existing logic)
    // ...
}

func (s *Simulator) writeToWAL(sizeMB float64) float64 {
    // WAL is sequential write (fast)
    ioTime := sizeMB / s.config.IOThroughputMBps

    // Optional fsync overhead
    syncTime := 0.0
    if s.config.WALSync {
        syncTime = s.config.WALSyncLatencyMs / 1000.0
    }

    return ioTime + syncTime
}
```

**RocksDB Reference:** `db/db_impl/db_impl_write.cc` lines 500-600
- WAL append: `log_writer_->AddRecord(batch.Data())`
- fsync: `log_->Sync()` (1ms overhead typical)

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // WAL configuration
    EnableWAL        bool    `json:"enableWAL"`         // Enable WAL tracking (default true)
    WALSync          bool    `json:"walSync"`           // Sync WAL after each write (default false)
    WALSyncLatencyMs float64 `json:"walSyncLatencyMs"`  // fsync latency (default 1.0ms)
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        EnableWAL:        true,
        WALSync:          false, // Async by default (like RocksDB)
        WALSyncLatencyMs: 1.0,
    }
}
```

#### UI Components

**Config Panel:**
- ☑ Enable WAL tracking
- ☑ Sync WAL on every write (slower, safer)
- 🎚️ WAL sync latency: 0.1ms - 10ms

**Metrics Display:**
- WAL Bytes Written: XXX MB
- WAL % of Total I/O: XX%
- Write Amplification (with WAL): X.XX

**Chart Addition:**
- Stacked area chart: WAL writes (green) + Compaction writes (blue) + Flush writes (orange)

#### Testing

**Unit Test:**
```go
func TestWALTracking(t *testing.T) {
    config := DefaultConfig()
    config.EnableWAL = true
    config.WriteRateMBps = 10.0

    sim, _ := NewSimulator(config)
    sim.Run(10.0) // 10 seconds

    // WAL bytes should equal user writes
    userWrites := 10.0 * 10 // 100 MB
    assert.InDelta(t, userWrites, sim.Metrics().WALBytesWritten, 1.0)

    // Write amp should increase by ~1.0x (WAL overhead)
    assert.Greater(t, sim.Metrics().WriteAmplification, 2.0) // At least 1x WAL + 1x flush
}
```

**Validation:**
Run RocksDB db_bench and compare:
- `rocksdb::Statistics::WAL_FILE_BYTES` vs simulator `WALBytesWritten`
- Should match within 5%

#### Time Estimate
**4-6 hours**
- 2 hours: Implementation
- 1 hour: Config + UI
- 1-2 hours: Testing
- 1 hour: Documentation

---

## 2. CPU Modeling

### Current Gap
- Compaction duration based purely on I/O time
- CPU-bound workloads (compression) not modeled
- Throughput predictions optimistic by 30-70%

### The Challenge

CPU affects **both read and write paths:**

**Write Path (Compaction):**
- Decompress input SSTs (read old files)
- Merge/deduplicate keys (CPU-intensive)
- Compress output SSTs (write new files)

**Read Path:**
- Decompress blocks for point lookups
- Decompress blocks for range scans

**Contention:**
- Multiple compactions compete for CPU cores
- Reads and compactions share CPU resources

### Implementation: Option A (Simple, Recommended)

**Approach:** Track CPU time separately, take `max(IO_time, CPU_time)` for each operation.

#### Go Library Structure

```go
// In simulator/cpu.go
type CPUTracker struct {
    totalCPUTime      float64 // Total CPU time consumed
    cpuBusyUntil      float64 // CPU available after this timestamp
    compressionThroughput   float64 // MB/s
    decompressionThroughput float64 // MB/s
}

// Calibrated compression throughput (single-threaded, Core i7)
var compressionThroughputMBps = map[string]float64{
    "none":   10000.0, // No compression
    "snappy": 500.0,   // Fast compression
    "lz4":    600.0,   // Fast compression
    "zstd":   200.0,   // Balanced (level 1)
    "zstd3":  100.0,   // Medium (level 3)
    "zstd9":  30.0,    // Slow (level 9)
}

var decompressionThroughputMBps = map[string]float64{
    "none":   10000.0,
    "snappy": 2000.0,
    "lz4":    3000.0,
    "zstd":   800.0,  // Same for all zstd levels
    "zstd3":  800.0,
    "zstd9":  800.0,
}

func (cpu *CPUTracker) CompressTime(sizeMB float64, algorithm string) float64 {
    throughput := compressionThroughputMBps[algorithm]
    return sizeMB / throughput
}

func (cpu *CPUTracker) DecompressTime(sizeMB float64, algorithm string) float64 {
    throughput := decompressionThroughputMBps[algorithm]
    return sizeMB / throughput
}

func (cpu *CPUTracker) ReserveCPU(duration float64, currentTime float64) float64 {
    // CPU operates in parallel with I/O, but is a separate bottleneck
    startTime := max(currentTime, cpu.cpuBusyUntil)
    cpu.cpuBusyUntil = startTime + duration
    cpu.totalCPUTime += duration
    return startTime + duration
}
```

#### Integration: Compaction Execution

Modify `leveled_compaction.go:executeCompactionSingle()`:

```go
func (c *LeveledCompactor) executeCompactionSingle(job *CompactionJob, lsm *LSMTree, config SimConfig) (float64, float64, int) {
    // Calculate sizes
    inputSize := calculateInputSize(job)
    outputSize := calculateOutputSize(job, inputSize, config)

    // EXISTING: I/O time calculation
    ioTime := (inputSize + outputSize) / config.IOThroughputMBps
    seekTime := config.IOLatencyMs / 1000.0
    ioTotalTime := ioTime + seekTime

    // NEW: CPU time calculation
    algorithm := config.CompressionAlgorithm

    // Decompress input files (read old data)
    decompressTime := lsm.cpuTracker.DecompressTime(inputSize, algorithm)

    // Compress output files (write new data)
    compressTime := lsm.cpuTracker.CompressTime(outputSize, algorithm)

    // Merge overhead (keys comparison, typically 10% of decompression time)
    mergeTime := decompressTime * 0.1

    cpuTotalTime := decompressTime + compressTime + mergeTime

    // Compaction duration = MAX of I/O and CPU
    duration := max(ioTotalTime, cpuTotalTime)

    // Log bottleneck
    if cpuTotalTime > ioTotalTime {
        fmt.Printf("[COMPACTION] CPU-bound: %.2fs CPU vs %.2fs I/O (%.1f%% CPU utilization)\n",
            cpuTotalTime, ioTotalTime, (cpuTotalTime/duration)*100)
        lsm.metrics.CPUBoundCompactions++
    } else {
        fmt.Printf("[COMPACTION] I/O-bound: %.2fs I/O vs %.2fs CPU (%.1f%% I/O utilization)\n",
            ioTotalTime, cpuTotalTime, (ioTotalTime/duration)*100)
        lsm.metrics.IOBoundCompactions++
    }

    // Reserve both resources
    lsm.cpuTracker.ReserveCPU(cpuTotalTime, currentTime)
    // diskBusyUntil already updated in existing code

    return inputSize, outputSize, outputFileCount
}
```

**RocksDB Reference:** `db/compaction/compaction_job.cc` lines 450-650
- Compression: `compress_timer.Start()` / `Compress(block_data)` / `compress_timer.Stop()`
- Measures CPU time separately from I/O time
- Reports `compaction_time_cpu_micros` and `compaction_time_noncpu_micros`

#### Integration: Read Path (Future)

When read path is implemented:

```go
func (s *Simulator) readSSTBlock(file *SSTFile, blockSize float64) {
    // I/O time
    ioTime := blockSize / s.config.IOThroughputMBps
    seekTime := s.config.IOLatencyMs / 1000.0

    // CPU time (decompression)
    cpuTime := s.cpuTracker.DecompressTime(blockSize, s.config.CompressionAlgorithm)

    // Read duration = MAX of I/O and CPU
    duration := max(ioTime + seekTime, cpuTime)

    // Reserve both resources
    s.diskBusyUntil = max(s.virtualTime, s.diskBusyUntil) + ioTime + seekTime
    s.cpuTracker.ReserveCPU(cpuTime, s.virtualTime)
}
```

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Compression configuration
    CompressionAlgorithm string `json:"compressionAlgorithm"` // "none", "snappy", "lz4", "zstd", "zstd3", "zstd9"
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        CompressionAlgorithm: "snappy", // RocksDB default
    }
}
```

#### Metrics Additions

```go
type Metrics struct {
    // ... existing fields ...

    // CPU metrics
    TotalCPUTimeUsed     float64 `json:"totalCPUTimeUsed"`     // Total CPU seconds consumed
    CPUUtilization       float64 `json:"cpuUtilization"`       // % of time CPU is busy
    CPUBoundCompactions  int     `json:"cpuBoundCompactions"`  // Count of CPU-bound compactions
    IOBoundCompactions   int     `json:"ioBoundCompactions"`   // Count of I/O-bound compactions
}

func (m *Metrics) updateCPUUtilization(virtualTime float64, cpuTracker *CPUTracker) {
    if virtualTime > 0 {
        m.CPUUtilization = (cpuTracker.totalCPUTime / virtualTime) * 100.0
    }
}
```

#### UI Components

**Config Panel:**
- 📦 Compression Algorithm: [Dropdown: None, Snappy, LZ4, Zstd (Level 1), Zstd (Level 3), Zstd (Level 9)]
- ℹ️ Info: "Affects CPU usage and space amplification"

**Metrics Display:**
- CPU Utilization: XX%
- CPU-Bound Compactions: XXX
- I/O-Bound Compactions: XXX
- Bottleneck: [CPU / I/O / Balanced]

**Chart Addition:**
- Dual-axis chart:
  - Left axis: Disk throughput (MB/s)
  - Right axis: CPU utilization (%)
  - Shows whether system is CPU or I/O limited over time

#### Testing

**Unit Test:**
```go
func TestCPUBottleneck(t *testing.T) {
    // Test CPU-bound workload (slow compression, fast disk)
    config := DefaultConfig()
    config.CompressionAlgorithm = "zstd9" // Very slow compression (30 MB/s)
    config.IOThroughputMBps = 500.0       // Fast disk
    config.WriteRateMBps = 50.0

    sim, _ := NewSimulator(config)
    sim.Run(30.0)

    // Should be CPU-bound (most compactions limited by CPU)
    metrics := sim.Metrics()
    assert.Greater(t, metrics.CPUBoundCompactions, metrics.IOBoundCompactions)
    assert.Greater(t, metrics.CPUUtilization, 50.0) // High CPU usage

    // Test I/O-bound workload (no compression, slow disk)
    config2 := DefaultConfig()
    config2.CompressionAlgorithm = "none"
    config2.IOThroughputMBps = 50.0 // Slow disk

    sim2, _ := NewSimulator(config2)
    sim2.Run(30.0)

    metrics2 := sim2.Metrics()
    assert.Greater(t, metrics2.IOBoundCompactions, metrics2.CPUBoundCompactions)
    assert.Less(t, metrics2.CPUUtilization, 20.0) // Low CPU usage
}
```

**Validation:**
- Compare simulator compaction duration vs RocksDB's `compaction_time_cpu_micros` + `compaction_time_noncpu_micros`
- Should match within 15% (compression has variance)

#### Time Estimate
**6-8 hours**
- 3 hours: Implementation (CPU tracker, integration)
- 1 hour: Calibration data (run RocksDB benchmarks)
- 2 hours: Config + UI
- 2 hours: Testing

---

## 3. Compression vs Deduplication

### Current State

**Existing:** `CompactionReductionFactor` (default 0.9 = 10% reduction)
- Handles tombstone removal
- Handles merge operator deduplication (multiple updates → single value)
- Example: 100 updates to same key → 1 final value

**Question:** Is compression different, or can we use this factor?

### Answer: They Are Different

**Deduplication (Logical):**
- Fewer keys/values after merge
- Happens during compaction merge phase
- Example: 1000 keys with tombstones → 900 keys (10% deduplication)

**Compression (Physical):**
- Fewer bytes on disk
- Happens after merge, when writing SST files
- Example: 100 MB uncompressed → 50 MB compressed (50% compression with Snappy)

**Both Apply:**
```
Input: 100 MB, 1000 keys
After deduplication: 90 MB, 900 keys (10% reduction)
After compression: 45 MB on disk (50% reduction)
Final size: 100 * 0.9 * 0.5 = 45 MB
```

### Implementation

#### Config Changes

```go
type SimConfig struct {
    // ... existing fields ...

    // Deduplication (logical reduction)
    CompactionReductionFactor float64 `json:"compactionReductionFactor"` // EXISTING - keep as-is

    // Compression (physical reduction)
    CompressionAlgorithm string `json:"compressionAlgorithm"` // NEW - "none", "snappy", "zstd", etc.
}

// Compression ratios (calibrated from RocksDB)
var compressionRatios = map[string]float64{
    "none":   1.0,  // No compression
    "snappy": 0.5,  // 2x compression ratio
    "lz4":    0.55, // ~2x compression ratio
    "zstd":   0.4,  // 2.5x compression ratio (level 1)
    "zstd3":  0.35, // ~3x compression ratio (level 3)
    "zstd9":  0.25, // 4x compression ratio (level 9)
}

func (c SimConfig) GetCompressionRatio() float64 {
    return compressionRatios[c.CompressionAlgorithm]
}
```

#### Update Compaction Execution

Modify `leveled_compaction.go:executeCompactionSingle()`:

```go
func (c *LeveledCompactor) executeCompactionSingle(job *CompactionJob, lsm *LSMTree, config SimConfig) (float64, float64, int) {
    inputSize := calculateInputSize(job)

    // Apply BOTH deduplication and compression
    dedupFactor := config.CompactionReductionFactor   // 0.9 = 10% dedup
    compressionFactor := config.GetCompressionRatio() // 0.5 = 50% compression

    outputSize := inputSize * dedupFactor * compressionFactor

    // Example: 100 MB input
    // After dedup: 100 * 0.9 = 90 MB
    // After compression: 90 * 0.5 = 45 MB
    // Final: 45 MB on disk

    fmt.Printf("[COMPACTION] Input=%.1fMB, After dedup=%.1fMB (%.0f%%), After compression=%.1fMB (%.0f%%)\n",
        inputSize,
        inputSize * dedupFactor, dedupFactor * 100,
        outputSize, compressionFactor * 100)

    return inputSize, outputSize, outputFileCount
}
```

**RocksDB Reference:**
- Compression: `table/block_based/block_based_table_builder.cc` lines 450-500
- Applied after merge: `Compress(block_data)` → writes compressed bytes

#### UI Components

**Config Panel:**
- 🎚️ **Deduplication Factor**: 0% - 50% (slider)
  - Label: "Logical key reduction (tombstones, merges)"
  - Default: 10%

- 📦 **Compression Algorithm**: [Dropdown]
  - Options: None, Snappy (2x), LZ4 (2x), Zstd L1 (2.5x), Zstd L3 (3x), Zstd L9 (4x)
  - Label: "Physical bytes reduction"
  - Default: Snappy

**Metrics Display:**
- Deduplication: XX% (logical)
- Compression: XX% (physical)
- Combined Reduction: XX%

#### Testing

```go
func TestCompressionAndDeduplication(t *testing.T) {
    config := DefaultConfig()
    config.CompactionReductionFactor = 0.9  // 10% dedup
    config.CompressionAlgorithm = "snappy"   // 50% compression
    config.WriteRateMBps = 10.0

    sim, _ := NewSimulator(config)
    sim.Run(30.0)

    // Final file sizes should reflect BOTH factors
    totalFlushed := sim.Metrics().TotalFlushWritten
    totalOnDisk := sim.lsm.GetTotalSize()

    // Expected: totalOnDisk ~= totalFlushed * 0.9 * 0.5 (after compactions settle)
    expectedReduction := 0.9 * 0.5 // 0.45 = 55% total reduction
    assert.InDelta(t, totalFlushed * expectedReduction, totalOnDisk, totalOnDisk * 0.2) // Within 20%
}
```

#### Time Estimate
**2 hours**
- 1 hour: Update config and compression ratios
- 30 min: Modify compaction execution
- 30 min: Update UI

---

## 4. Write Controller (Dynamic Throttling)

### Current Gap
- Hard stalls implemented (stop writes when buffers full)
- Soft delays NOT implemented (gradual slowdown before hard stop)
- Simulator shows cliff-edge behavior (100% → 0%), RocksDB shows gradual degradation (100% → 50% → 25% → 0%)

### Implementation

#### Go Library Structure

```go
// In simulator/write_controller.go
type WriteController struct {
    delayedWriteRate float64  // Current throttled rate (MB/s), 0 = no throttle
    creditBytes      float64  // Credit balance for rate limiting
    nextRefillTime   float64  // Next credit refill time
    totalDelayed     int      // Number of delayed writes
    totalStopped     int      // Number of stopped writes
}

func NewWriteController() *WriteController {
    return &WriteController{
        delayedWriteRate: 0.0,
        creditBytes:      0.0,
        nextRefillTime:   0.0,
    }
}

// Credit-based rate limiting (matches RocksDB's algorithm)
func (wc *WriteController) GetDelay(virtualTime float64, numBytes float64) float64 {
    const kMicrosPerRefill = 0.001 // 1ms refill interval

    // If writes are stopped, return infinite delay
    if wc.totalStopped > 0 {
        return math.Inf(1)
    }

    // If no throttling, return 0
    if wc.totalDelayed == 0 || wc.delayedWriteRate == 0 {
        return 0.0
    }

    // Check credit balance
    if wc.creditBytes >= numBytes {
        wc.creditBytes -= numBytes
        return 0.0
    }

    // Refill credit if time has passed
    if wc.nextRefillTime == 0 {
        wc.nextRefillTime = virtualTime
    }

    if wc.nextRefillTime <= virtualTime {
        elapsed := virtualTime - wc.nextRefillTime + kMicrosPerRefill
        wc.creditBytes += elapsed * wc.delayedWriteRate
        wc.nextRefillTime = virtualTime + kMicrosPerRefill

        if wc.creditBytes >= numBytes {
            wc.creditBytes -= numBytes
            return 0.0
        }
    }

    // Calculate delay needed
    bytesOverBudget := numBytes - wc.creditBytes
    delay := bytesOverBudget / wc.delayedWriteRate

    wc.creditBytes = 0
    wc.nextRefillTime += delay

    return max(delay, kMicrosPerRefill)
}

func (wc *WriteController) SetDelayedWriteRate(rateMBps float64) {
    wc.delayedWriteRate = rateMBps
    wc.totalDelayed++
}

func (wc *WriteController) SetStopped() {
    wc.totalStopped++
}

func (wc *WriteController) ClearStopped() {
    if wc.totalStopped > 0 {
        wc.totalStopped--
    }
}

func (wc *WriteController) ClearDelayed() {
    wc.totalDelayed = 0
    wc.delayedWriteRate = 0.0
}
```

**RocksDB Reference:** `db/write_controller.cc` lines 51-99
- Exact credit-based token bucket algorithm
- Refills every 1ms at `delayed_write_rate`

#### Integration Point

Modify `simulator.go:processWriteEvent()`:

```go
func (s *Simulator) processWriteEvent(event *WriteEvent) {
    // Update write controller state based on LSM tree
    s.updateWriteController()

    // Check if write should be delayed or stopped
    delay := s.writeController.GetDelay(s.virtualTime, event.SizeMB)

    if math.IsInf(delay, 1) {
        // Write is stopped, reschedule for later
        s.queue.Push(NewWriteEvent(s.virtualTime + 0.1, event.SizeMB)) // Retry in 100ms
        s.metrics.StoppedWrites++
        return
    }

    if delay > 0 {
        // Write is delayed, reschedule
        s.queue.Push(NewWriteEvent(s.virtualTime + delay, event.SizeMB))
        s.metrics.DelayedWrites++
        s.metrics.TotalDelayedSec += delay
        return
    }

    // Write proceeds normally
    // ... existing WAL, memtable logic ...
}

func (s *Simulator) updateWriteController() {
    l0FileCount := s.lsm.Levels[0].FileCount

    // RocksDB thresholds
    slowdownTrigger := s.config.L0SlowdownTrigger  // e.g., 20 files
    stopTrigger := s.config.L0StopTrigger          // e.g., 30 files

    if l0FileCount >= stopTrigger {
        // STOP writes completely
        if s.writeController.totalStopped == 0 {
            s.writeController.SetStopped()
            fmt.Printf("[WRITE STALL] L0 files (%d) >= stop trigger (%d), STOPPING writes\n",
                l0FileCount, stopTrigger)
        }
    } else if l0FileCount >= slowdownTrigger {
        // DELAY writes (gradual slowdown)
        if s.writeController.totalStopped > 0 {
            s.writeController.ClearStopped()
        }

        // Calculate delayed write rate
        // RocksDB formula: rate decreases linearly as L0 fills
        // rate = base_rate * (stop_trigger - l0_count) / (stop_trigger - slowdown_trigger)
        slowdownFactor := float64(stopTrigger - l0FileCount) / float64(stopTrigger - slowdownTrigger)
        delayedRate := s.config.WriteRateMBps * slowdownFactor

        s.writeController.SetDelayedWriteRate(delayedRate)
        fmt.Printf("[WRITE SLOWDOWN] L0 files (%d) >= slowdown trigger (%d), rate=%.1f MB/s (%.0f%%)\n",
            l0FileCount, slowdownTrigger, delayedRate, slowdownFactor * 100)
    } else {
        // NO throttling
        if s.writeController.totalStopped > 0 {
            s.writeController.ClearStopped()
            fmt.Printf("[WRITE RESUME] L0 files (%d) < slowdown trigger (%d), resuming full rate\n",
                l0FileCount, slowdownTrigger)
        }
        if s.writeController.totalDelayed > 0 {
            s.writeController.ClearDelayed()
        }
    }
}
```

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Write throttling thresholds
    L0SlowdownTrigger int `json:"l0SlowdownTrigger"` // Start gradual slowdown (default 20)
    L0StopTrigger     int `json:"l0StopTrigger"`     // Stop writes completely (default 30)
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        L0CompactionTrigger: 4,   // Start compacting (existing)
        L0SlowdownTrigger:   20,  // Start throttling (NEW)
        L0StopTrigger:       30,  // Stop writes (NEW)
    }
}
```

#### Metrics Additions

```go
type Metrics struct {
    // ... existing fields ...

    // Write throttling metrics
    DelayedWrites    int     `json:"delayedWrites"`    // Count of delayed writes
    StoppedWrites    int     `json:"stoppedWrites"`    // Count of stopped writes
    TotalDelayedSec  float64 `json:"totalDelayedSec"`  // Total delay time
    CurrentThrottle  float64 `json:"currentThrottle"`  // Current throttle rate (MB/s), 0 = no throttle
}
```

#### UI Components

**Config Panel:**
- 🎚️ **L0 Slowdown Trigger**: 4 - 40 files (default 20)
  - Info: "Start gradual write slowdown"

- 🎚️ **L0 Stop Trigger**: 10 - 50 files (default 30)
  - Info: "Stop writes completely"

**Metrics Display:**
- Delayed Writes: XXX
- Stopped Writes: XXX
- Total Delay Time: XX.X seconds
- Current Throttle: XX% (or "Not throttled")

**Chart Addition:**
- Line chart: Write rate over time
  - Shows full rate → gradual slowdown → stop → resume
  - Color-coded: Green (full), Yellow (delayed), Red (stopped)

#### Testing

```go
func TestWriteThrottling(t *testing.T) {
    config := DefaultConfig()
    config.L0CompactionTrigger = 4
    config.L0SlowdownTrigger = 8
    config.L0StopTrigger = 12
    config.WriteRateMBps = 100.0
    config.IOThroughputMBps = 50.0 // Slow compactions, writes will pile up

    sim, _ := NewSimulator(config)

    // Run until throttling happens
    for i := 0; i < 1000; i++ {
        sim.Step()

        l0Count := sim.lsm.Levels[0].FileCount
        metrics := sim.Metrics()

        if l0Count >= 8 && l0Count < 12 {
            // Should be delaying writes
            assert.Greater(t, metrics.DelayedWrites, 0, "Should have delayed writes")
            assert.Less(t, metrics.CurrentThrottle, 100.0, "Should be throttled")
        }

        if l0Count >= 12 {
            // Should be stopping writes
            assert.Greater(t, metrics.StoppedWrites, 0, "Should have stopped writes")
            break
        }
    }
}
```

**Validation:**
- Compare simulator delayed/stopped write counts vs RocksDB's `STALL_MICROS`
- Compare throttle rate vs RocksDB's `delayed_write_rate`
- Should match within 10%

#### Time Estimate
**4-6 hours**
- 2 hours: Implementation (WriteController)
- 1 hour: Integration with existing stall logic
- 1 hour: Config + UI
- 1-2 hours: Testing

---

## 5. Read Path Implementation

### Current Gap
- No read operations simulated
- Cannot model read amplification, read latency, or mixed workloads
- Bloom filter and block cache features blocked

### Implementation

#### Go Library Structure

```go
// In simulator/events.go
type ReadEvent struct {
    BaseEvent
    key       string  // Key being looked up (or empty for scan)
    isRange   bool    // Point lookup vs range scan
    rangeSize float64 // For range scans, size in MB
}

func NewPointLookupEvent(timestamp float64, key string) *ReadEvent {
    return &ReadEvent{
        BaseEvent: BaseEvent{timestamp: timestamp},
        key:       key,
        isRange:   false,
    }
}

func NewRangeScanEvent(timestamp float64, sizeMB float64) *ReadEvent {
    return &ReadEvent{
        BaseEvent: BaseEvent{timestamp: timestamp},
        isRange:   true,
        rangeSize: sizeMB,
    }
}
```

#### Integration Point

Add to `simulator.go`:

```go
func (s *Simulator) processReadEvent(event *ReadEvent) {
    if event.isRange {
        s.processRangeScan(event)
    } else {
        s.processPointLookup(event)
    }
}

func (s *Simulator) processPointLookup(event *ReadEvent) {
    filesAccessed := 0

    // 1. Check memtable (no I/O, instant)
    if s.lsm.memtableContains(event.key) {
        s.metrics.MemtableHits++
        return // Found in memtable
    }

    // 2. Check immutable memtables (no I/O)
    for i := 0; i < s.numImmutableMemtables; i++ {
        s.metrics.ImmutableMemtableChecks++
        // Probabilistic: assume 5% hit rate per immutable memtable
        if rand.Float64() < 0.05 {
            s.metrics.ImmutableMemtableHits++
            return
        }
    }

    // 3. Check L0 files (ALL must be checked - tiered, overlapping)
    for _, file := range s.lsm.Levels[0].Files {
        // Apply bloom filter (if implemented)
        if s.config.BloomBitsPerKey > 0 {
            if !s.bloomFilterMayContain(file, event.key) {
                s.metrics.BloomFilterFiltered++
                continue // Bloom says "definitely not present"
            }
        }

        // Check block cache (if implemented)
        if s.config.BlockCacheSizeMB > 0 {
            if s.blockCache.contains(file, event.key) {
                s.metrics.BlockCacheHits++
                return // Found in cache
            }
        }

        // Read block from disk
        s.readSSTBlock(file, 0.004) // 4KB block
        filesAccessed++
        s.metrics.BlockCacheMisses++
    }

    // 4. Binary search L1+ levels (non-overlapping)
    for level := 1; level < len(s.lsm.Levels); level++ {
        file := s.findFileInLevel(level, event.key)
        if file == nil {
            continue // Key not in this level
        }

        // Apply bloom filter
        if s.config.BloomBitsPerKey > 0 {
            if !s.bloomFilterMayContain(file, event.key) {
                s.metrics.BloomFilterFiltered++
                continue
            }
        }

        // Check block cache
        if s.config.BlockCacheSizeMB > 0 {
            if s.blockCache.contains(file, event.key) {
                s.metrics.BlockCacheHits++
                return
            }
        }

        // Read from disk
        s.readSSTBlock(file, 0.004)
        filesAccessed++
        s.metrics.BlockCacheMisses++
    }

    // Update read amplification
    s.metrics.ReadAmp += float64(filesAccessed)
    s.metrics.TotalReads++

    if s.metrics.TotalReads > 0 {
        s.metrics.AvgReadAmp = s.metrics.ReadAmp / float64(s.metrics.TotalReads)
    }
}

func (s *Simulator) findFileInLevel(level int, key string) *SSTFile {
    // Binary search for file containing key
    // Simplified: Use hash to deterministically pick a file
    files := s.lsm.Levels[level].Files
    if len(files) == 0 {
        return nil
    }

    // Probabilistic model: 10% chance key is in this level
    if rand.Float64() > 0.1 {
        return nil
    }

    // Pick a random file (in real RocksDB, this is based on key ranges)
    idx := int(hashString(key)) % len(files)
    return files[idx]
}

func (s *Simulator) readSSTBlock(file *SSTFile, blockSizeMB float64) {
    // I/O time
    seekTime := s.config.IOLatencyMs / 1000.0
    readTime := blockSizeMB / s.config.IOThroughputMBps
    ioTime := seekTime + readTime

    // CPU time (decompression, if CPU modeling enabled)
    cpuTime := 0.0
    if s.config.CompressionAlgorithm != "none" {
        cpuTime = s.cpuTracker.DecompressTime(blockSizeMB, s.config.CompressionAlgorithm)
    }

    // Read duration = MAX of I/O and CPU
    duration := max(ioTime, cpuTime)

    // Reserve disk bandwidth (competes with writes/compactions)
    s.diskBusyUntil = max(s.virtualTime, s.diskBusyUntil) + ioTime

    // Reserve CPU (if CPU modeling enabled)
    if cpuTime > 0 {
        s.cpuTracker.ReserveCPU(cpuTime, s.virtualTime)
    }

    s.metrics.DiskBytesRead += blockSizeMB

    // Add to block cache (if enabled)
    if s.config.BlockCacheSizeMB > 0 {
        s.blockCache.add(file.ID + ":block", blockSizeMB)
    }
}
```

**RocksDB Reference:** `table/block_based/block_based_table_reader.cc` lines 2000-2100
- Point lookup: `Get(ReadOptions, key)`
- Bloom filter: `filter_->KeyMayMatch(key)`
- Block cache: `block_cache_->Lookup(block_key)`

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Read workload configuration
    ReadRateQPS      float64 `json:"readRateQPS"`      // Read query rate (queries per second)
    ReadWorkloadType string  `json:"readWorkloadType"` // "point_lookup", "range_scan", "mixed"
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        ReadRateQPS:      0.0,    // Disabled by default (write-only)
        ReadWorkloadType: "point_lookup",
    }
}
```

#### Metrics Additions

```go
type Metrics struct {
    // ... existing fields ...

    // Read metrics
    TotalReads             int     `json:"totalReads"`
    ReadAmp                float64 `json:"readAmp"`                // Total files accessed
    AvgReadAmp             float64 `json:"avgReadAmp"`             // Avg files per read
    MemtableHits           int     `json:"memtableHits"`
    ImmutableMemtableHits  int     `json:"immutableMemtableHits"`
    ImmutableMemtableChecks int    `json:"immutableMemtableChecks"`
    DiskBytesRead          float64 `json:"diskBytesRead"`
}
```

#### UI Components

**Config Panel:**
- 🎚️ **Read Rate**: 0 - 10,000 QPS
  - Info: "Queries per second"

- 📦 **Read Workload**: [Dropdown: Point Lookups, Range Scans, Mixed]

**Metrics Display:**
- Total Reads: XXX
- Avg Read Amplification: X.XX files/read
- Memtable Hit Rate: XX%
- Disk Reads: XXX MB

**Chart Addition:**
- Dual-axis chart:
  - Left axis: Read throughput (QPS)
  - Right axis: Read amplification (files/read)

#### Testing

```go
func TestReadPath(t *testing.T) {
    config := DefaultConfig()
    config.WriteRateMBps = 10.0
    config.ReadRateQPS = 100.0 // 100 reads/sec
    config.ReadWorkloadType = "point_lookup"

    sim, _ := NewSimulator(config)
    sim.Run(10.0) // 10 seconds

    metrics := sim.Metrics()

    // Should have ~1000 reads (100 QPS * 10 sec)
    assert.InDelta(t, 1000.0, float64(metrics.TotalReads), 100.0)

    // Read amp should be > 1 (multiple files checked)
    assert.Greater(t, metrics.AvgReadAmp, 1.0)

    // Memtable should have some hits
    assert.Greater(t, metrics.MemtableHits, 0)
}
```

#### Time Estimate
**8-10 hours**
- 4 hours: Implementation (read events, lookup logic)
- 2 hours: Integration with LSM structure
- 2 hours: Config + UI
- 2 hours: Testing

---

## 6. Bloom Filters

### Current Gap
- All reads check all files (100% false positive rate)
- Read amplification overestimated by 10-50x

### Implementation

**Depends on:** Read Path (Section 5)

#### Go Library Structure

```go
// In simulator/bloom.go
type BloomFilter struct {
    bitsPerKey      int     // bits_per_key setting (default 10)
    falsePositiveRate float64 // Computed FP rate
}

func NewBloomFilter(bitsPerKey int) *BloomFilter {
    // False positive rate formula: FP ≈ (0.6185)^bits_per_key
    fpRate := math.Pow(0.6185, float64(bitsPerKey))

    return &BloomFilter{
        bitsPerKey:        bitsPerKey,
        falsePositiveRate: fpRate,
    }
}

// Probabilistic check (no actual bloom filter bits)
func (bf *BloomFilter) MayContain(key string) bool {
    // Return false with probability (1 - FP rate)
    // This means bloom filter "filters out" (1 - FP rate) of negative lookups
    return rand.Float64() < bf.falsePositiveRate
}

// Add bloom filter metadata to SST files
type SSTFile struct {
    // ... existing fields ...
    BloomFilter *BloomFilter // Bloom filter for this file
}
```

#### Integration Point

Add bloom filter to file creation (`lsm.go:FlushMemtable`):

```go
func (lsm *LSMTree) FlushMemtable(timestamp float64) *SSTFile {
    file := &SSTFile{
        ID:        generateFileID(),
        SizeMB:    lsm.memtableSizeMB,
        CreatedAt: timestamp,
    }

    // Add bloom filter (if configured)
    if lsm.config.BloomBitsPerKey > 0 {
        file.BloomFilter = NewBloomFilter(lsm.config.BloomBitsPerKey)
    }

    lsm.Levels[0].AddFile(file)
    return file
}
```

Modify read path to use bloom filters (already shown in Section 5):

```go
func (s *Simulator) bloomFilterMayContain(file *SSTFile, key string) bool {
    if file.BloomFilter == nil {
        return true // No bloom filter, must check file
    }

    // Probabilistic check
    mayContain := file.BloomFilter.MayContain(key)

    if !mayContain {
        s.metrics.BloomFilterFiltered++
    } else {
        s.metrics.BloomFilterUseful++
    }

    return mayContain
}
```

**RocksDB Reference:** `table/block_based/full_filter_block.cc` lines 150-200
- Bloom filter check: `FullFilterBlockReader::KeyMayMatch(key)`
- False positive formula matches RocksDB's implementation

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Bloom filter configuration
    BloomBitsPerKey int `json:"bloomBitsPerKey"` // bits per key (0 = disabled, 10 = default)
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        BloomBitsPerKey: 10, // RocksDB default (0.8% FP rate)
    }
}
```

#### Metrics Additions

```go
type Metrics struct {
    // ... existing fields ...

    // Bloom filter metrics
    BloomFilterFiltered int `json:"bloomFilterFiltered"` // Files skipped due to bloom
    BloomFilterUseful   int `json:"bloomFilterUseful"`   // Files checked (bloom said maybe)
}

func (m *Metrics) GetBloomFilterHitRate() float64 {
    total := m.BloomFilterFiltered + m.BloomFilterUseful
    if total > 0 {
        return float64(m.BloomFilterFiltered) / float64(total) * 100.0
    }
    return 0.0
}
```

#### UI Components

**Config Panel:**
- 🎚️ **Bloom Filter Bits/Key**: 0 - 20 (default 10)
  - Info: "Higher = fewer false positives, more memory"
  - Labels: 0 (Disabled), 10 (1% FP), 20 (0.01% FP)

**Metrics Display:**
- Bloom Filter Hit Rate: XX% (files skipped)
- False Positive Rate: X.XX% (theoretical)
- Files Filtered: XXX
- Files Checked: XXX

#### Testing

```go
func TestBloomFilters(t *testing.T) {
    config := DefaultConfig()
    config.WriteRateMBps = 10.0
    config.ReadRateQPS = 1000.0
    config.BloomBitsPerKey = 10 // 0.8% FP rate

    sim, _ := NewSimulator(config)
    sim.Run(10.0)

    metrics := sim.Metrics()

    // Bloom filters should filter out most negative lookups
    // Hit rate should be ~99% (1 - 0.008 FP rate)
    bloomHitRate := metrics.GetBloomFilterHitRate()
    assert.Greater(t, bloomHitRate, 90.0) // At least 90% filtered

    // Read amp should be much lower than without blooms
    assert.Less(t, metrics.AvgReadAmp, 5.0) // Should check <5 files on average
}
```

**Validation:**
- Compare `BloomFilterFiltered / (BloomFilterFiltered + BloomFilterUseful)` vs RocksDB's `BLOOM_FILTER_USEFUL / BLOOM_FILTER_CHECKED`
- Should match within 10% (bloom filters have randomness)

#### Time Estimate
**2-3 hours**
- 1 hour: Implementation (BloomFilter struct, probabilistic logic)
- 30 min: Integration with read path
- 30 min: Config + UI
- 30 min: Testing

---

## 7. Block Cache

### Current Gap
- All reads hit disk (100% cache miss rate)
- Read latency overestimated by 10-100x

### Implementation

**Depends on:** Read Path (Section 5)

#### Go Library Structure

```go
// In simulator/block_cache.go
type BlockCache struct {
    sizeMB      float64               // Total cache size
    currentMB   float64               // Current used size
    blocks      map[string]*CacheEntry // Keyed by "fileID:blockOffset"
    lruQueue    []*CacheEntry         // LRU eviction queue
    hitCount    int
    missCount   int
}

type CacheEntry struct {
    key       string
    sizeMB    float64
    timestamp float64
}

func NewBlockCache(sizeMB float64) *BlockCache {
    return &BlockCache{
        sizeMB:   sizeMB,
        blocks:   make(map[string]*CacheEntry),
        lruQueue: make([]*CacheEntry, 0),
    }
}

func (cache *BlockCache) contains(file *SSTFile, key string) bool {
    // Simplified: assume 4KB blocks
    blockOffset := int(hashString(key)) % int(file.SizeMB * 256) // 256 blocks per MB (4KB each)
    cacheKey := fmt.Sprintf("%s:%d", file.ID, blockOffset)

    if entry, ok := cache.blocks[cacheKey]; ok {
        // Hit: move to front of LRU queue
        cache.moveToFront(entry)
        cache.hitCount++
        return true
    }

    // Miss: will be added after disk read
    cache.missCount++
    return false
}

func (cache *BlockCache) add(key string, sizeMB float64, timestamp float64) {
    // Evict oldest blocks if cache full
    for cache.currentMB + sizeMB > cache.sizeMB && len(cache.lruQueue) > 0 {
        oldest := cache.lruQueue[len(cache.lruQueue)-1]
        cache.evict(oldest)
    }

    // Add new block
    entry := &CacheEntry{key: key, sizeMB: sizeMB, timestamp: timestamp}
    cache.blocks[key] = entry
    cache.lruQueue = append([]*CacheEntry{entry}, cache.lruQueue...) // Add to front
    cache.currentMB += sizeMB
}

func (cache *BlockCache) evict(entry *CacheEntry) {
    delete(cache.blocks, entry.key)
    cache.currentMB -= entry.sizeMB

    // Remove from LRU queue
    for i, e := range cache.lruQueue {
        if e.key == entry.key {
            cache.lruQueue = append(cache.lruQueue[:i], cache.lruQueue[i+1:]...)
            break
        }
    }
}

func (cache *BlockCache) moveToFront(entry *CacheEntry) {
    // Remove from current position
    for i, e := range cache.lruQueue {
        if e.key == entry.key {
            cache.lruQueue = append(cache.lruQueue[:i], cache.lruQueue[i+1:]...)
            break
        }
    }

    // Add to front
    cache.lruQueue = append([]*CacheEntry{entry}, cache.lruQueue...)
}

func (cache *BlockCache) GetHitRate() float64 {
    total := cache.hitCount + cache.missCount
    if total > 0 {
        return float64(cache.hitCount) / float64(total) * 100.0
    }
    return 0.0
}
```

**RocksDB Reference:** `cache/lru_cache.cc` lines 300-500
- LRU eviction: `LRUCacheShard::Evict()`
- Cache lookup: `LRUCacheShard::Lookup(key)`

#### Integration Point

Add to simulator initialization:

```go
func NewSimulator(config SimConfig) (*Simulator, error) {
    // ... existing initialization ...

    // Initialize block cache
    var blockCache *BlockCache
    if config.BlockCacheSizeMB > 0 {
        blockCache = NewBlockCache(config.BlockCacheSizeMB)
    }

    return &Simulator{
        // ... existing fields ...
        blockCache: blockCache,
    }, nil
}
```

Modify read path to use cache (already shown in Section 5):

```go
func (s *Simulator) processPointLookup(event *ReadEvent) {
    // ... bloom filter check ...

    // Check block cache
    if s.blockCache != nil {
        if s.blockCache.contains(file, event.key) {
            s.metrics.BlockCacheHits++
            return // Found in cache, no disk I/O
        }
    }

    // Cache miss, read from disk
    s.readSSTBlock(file, 0.004)
    s.metrics.BlockCacheMisses++

    // Add to cache
    if s.blockCache != nil {
        s.blockCache.add(file.ID + ":" + event.key, 0.004, s.virtualTime)
    }
}
```

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Block cache configuration
    BlockCacheSizeMB int `json:"blockCacheSizeMB"` // Cache size (0 = disabled, 256 = default)
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        BlockCacheSizeMB: 256, // 256 MB default
    }
}
```

#### Metrics Additions

```go
type Metrics struct {
    // ... existing fields ...

    // Block cache metrics
    BlockCacheHits   int `json:"blockCacheHits"`
    BlockCacheMisses int `json:"blockCacheMisses"`
}

func (m *Metrics) GetBlockCacheHitRate() float64 {
    total := m.BlockCacheHits + m.BlockCacheMisses
    if total > 0 {
        return float64(m.BlockCacheHits) / float64(total) * 100.0
    }
    return 0.0
}
```

#### UI Components

**Config Panel:**
- 🎚️ **Block Cache Size**: 0 - 2048 MB (default 256)
  - Info: "Larger cache = better read performance"
  - Labels: 0 (Disabled), 256 (Default), 1024 (Large)

**Metrics Display:**
- Block Cache Hit Rate: XX%
- Cache Hits: XXX
- Cache Misses: XXX
- Cache Memory Used: XXX MB / XXX MB

**Chart Addition:**
- Line chart: Cache hit rate over time (%)

#### Testing

```go
func TestBlockCache(t *testing.T) {
    config := DefaultConfig()
    config.WriteRateMBps = 10.0
    config.ReadRateQPS = 1000.0
    config.BlockCacheSizeMB = 512 // Large cache

    sim, _ := NewSimulator(config)
    sim.Run(30.0) // Run for 30 seconds

    metrics := sim.Metrics()

    // With large cache, hit rate should be high
    hitRate := metrics.GetBlockCacheHitRate()
    assert.Greater(t, hitRate, 70.0) // At least 70% hit rate

    // Read latency should be much lower than without cache
    // (hard to test directly, but cache hits should be abundant)
    assert.Greater(t, metrics.BlockCacheHits, metrics.BlockCacheMisses)
}
```

**Validation:**
- Compare `BlockCacheHits / (BlockCacheHits + BlockCacheMisses)` vs RocksDB's `BLOCK_CACHE_HIT / (BLOCK_CACHE_HIT + BLOCK_CACHE_MISS)`
- Should match within 15% (cache behavior has variance)

#### Time Estimate
**3-5 hours**
- 2 hours: Implementation (LRU cache, eviction logic)
- 1 hour: Integration with read path
- 1 hour: Config + UI
- 1 hour: Testing

---

## 8. RocksDB-Style Logging

### Current Gap
- Simulator produces freehand/debug log messages
- Cannot directly compare simulator logs with RocksDB logs
- Missing structured "Compaction Stats" summary (critical for validation)
- Individual compaction messages don't match RocksDB format

### Why This Matters

**For Validation (Item #2 below):** When comparing simulator vs real RocksDB:
- Need matching log formats to automate comparison
- Compaction Stats table is THE standard format for performance analysis
- Real operators use these logs for production troubleshooting

**For Production Use:** DBAs expect RocksDB log format:
- Copy/paste simulator logs into bug reports
- Compare side-by-side with production logs
- Use existing log analysis tools

### Implementation

#### 8.1 Compaction Stats Table

**RocksDB Format** (from mork.log:1566-1576):
```
** Compaction Stats [default] **
Level    Files   Size     Score Read(GB)  Rn(GB) Rnp1(GB) Write(GB) Wnew(GB) Moved(GB) W-Amp Rd(MB/s) Wr(MB/s) Comp(sec) CompMergeCPU(sec) Comp(cnt) Avg(sec) KeyIn KeyDrop Rblob(GB) Wblob(GB)
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
  L0      2/0    4.33 KB   0.2      0.0     0.0      0.0       0.0      0.0       0.0   0.0      0.0      0.0      0.00              0.00         0    0.000       0      0       0.0       0.0
  L3      4/0   159.71 MB  12.5      0.2     0.1      0.1       0.2      0.1       0.0   2.9     82.1     80.8      1.98              1.75         1    1.977   4999K    79K       0.0       0.0
  L4      7/0   74.39 MB   0.6      0.0     0.0      0.0       0.0      0.0       0.0   0.0      0.0      0.0      0.00              0.00         0    0.000       0      0       0.0       0.0
  L5     57/0    1.25 GB   1.0      0.0     0.0      0.0       0.0      0.0       0.0   0.0      0.0      0.0      0.00              0.00         0    0.000       0      0       0.0       0.0
  L6    322/0   12.83 GB   0.0      0.0     0.0      0.0       0.0      0.0       0.0   0.0      0.0      0.0      0.00              0.00         0    0.000       0      0       0.0       0.0
 Sum    390/0   14.31 GB   0.0      0.2     0.1      0.1       0.2      0.1       0.0  39.0     78.6     79.3      2.07              1.75         2    1.033   4999K    79K       0.0       0.0
 Int      0/0    0.00 KB   0.0      0.2     0.1      0.1       0.2      0.1       0.0  39.0     78.6     79.3      2.07              1.75         2    1.033   4999K    79K       0.0       0.0

Uptime(secs): 181.9 total, 181.9 interval
Flush(GB): cumulative 0.004, interval 0.004
Cumulative compaction: 0.16 GB write, 0.90 MB/s write, 0.16 GB read, 0.89 MB/s read, 2.1 seconds
Interval compaction: 0.16 GB write, 0.90 MB/s write, 0.16 GB read, 0.89 MB/s read, 2.1 seconds
Write Stall (count): cf-l0-file-count-limit-delays-with-ongoing-compaction: 0, cf-l0-file-count-limit-stops-with-ongoing-compaction: 0, l0-file-count-limit-delays: 0, l0-file-count-limit-stops: 0, memtable-limit-delays: 0, memtable-limit-stops: 0, pending-compaction-bytes-delays: 0, pending-compaction-bytes-stops: 0, total-delays: 0, total-stops: 0
Block cache LRUCache@0xe4ecfd828190#1 capacity: 32.00 MB seed: 946141485 usage: 0.08 KB table_size: 1024 occupancy: 1 collections: 1 last_copies: 0 last_secs: 5.3e-05 secs_since: 0
```

**Go Implementation:**

```go
// In simulator/logging.go
type CompactionStatsLogger struct {
    startTime    float64
    lastLogTime  float64
    logInterval  float64 // Log every N seconds (default 600 = 10 min)

    // Per-level cumulative stats
    levelStats map[int]*LevelCompactionStats
}

type LevelCompactionStats struct {
    Level              int
    FileCount          int
    CompactingCount    int   // Files currently being compacted
    SizeMB             float64
    Score              float64
    ReadGB             float64  // Total bytes read from this level
    RnGB               float64  // Bytes read from Rn (source level)
    Rnp1GB             float64  // Bytes read from Rn+1 (target level)
    WriteGB            float64  // Total bytes written
    WnewGB             float64  // New bytes written (Write - Rnp1)
    MovedGB            float64  // Bytes moved (trivial moves)
    WAmp               float64  // Write amplification
    ReadMBps           float64  // Read throughput
    WriteMBps          float64  // Write throughput
    CompactionSec      float64  // Total compaction time
    CompactionCPUSec   float64  // CPU time in compaction
    CompactionCount    int      // Number of compactions
    AvgCompactionSec   float64  // Average compaction time
    KeysIn             int64    // Keys read
    KeysDropped        int64    // Keys dropped (deleted, overwritten)
}

func (logger *CompactionStatsLogger) LogCompactionStats(sim *Simulator) {
    // Print header
    fmt.Printf("** Compaction Stats [default] **\n")
    fmt.Printf("Level    Files   Size     Score Read(GB)  Rn(GB) Rnp1(GB) Write(GB) Wnew(GB) Moved(GB) W-Amp Rd(MB/s) Wr(MB/s) Comp(sec) CompMergeCPU(sec) Comp(cnt) Avg(sec) KeyIn KeyDrop Rblob(GB) Wblob(GB)\n")
    fmt.Printf("-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------\n")

    // Per-level stats
    var sumStats LevelCompactionStats
    for level := 0; level < len(sim.lsm.Levels); level++ {
        stats := logger.levelStats[level]
        if stats == nil {
            stats = &LevelCompactionStats{Level: level}
        }

        // Update current level state
        stats.FileCount = sim.lsm.Levels[level].FileCount
        stats.CompactingCount = sim.lsm.Levels[level].CompactingFileCount
        stats.SizeMB = sim.lsm.Levels[level].TotalSize
        stats.Score = sim.lsm.Levels[level].Score

        // Print level stats
        fmt.Printf("  L%d   %4d/%-3d %8s %5.1f   %7.1f %6.1f %8.1f %9.1f %8.1f %9.1f %5.1f %8.1f %8.1f %9.2f %17.2f %9d %7.3f %6s %6s %9.1f %9.1f\n",
            stats.Level,
            stats.FileCount, stats.CompactingCount,
            formatSize(stats.SizeMB),
            stats.Score,
            stats.ReadGB,
            stats.RnGB,
            stats.Rnp1GB,
            stats.WriteGB,
            stats.WnewGB,
            stats.MovedGB,
            stats.WAmp,
            stats.ReadMBps,
            stats.WriteMBps,
            stats.CompactionSec,
            stats.CompactionCPUSec,
            stats.CompactionCount,
            stats.AvgCompactionSec,
            formatKeys(stats.KeysIn),
            formatKeys(stats.KeysDropped),
            0.0, // Rblob - not implemented
            0.0) // Wblob - not implemented

        // Accumulate for sum row
        sumStats.accumulate(stats)
    }

    // Print sum row
    fmt.Printf(" Sum  %4d/%-3d %8s %5.1f   %7.1f %6.1f %8.1f %9.1f %8.1f %9.1f %5.1f %8.1f %8.1f %9.2f %17.2f %9d %7.3f %6s %6s %9.1f %9.1f\n",
        sumStats.FileCount, sumStats.CompactingCount,
        formatSize(sumStats.SizeMB),
        sumStats.Score,
        /* ... rest of sum stats ... */)

    // Print summary stats
    fmt.Printf("\nUptime(secs): %.1f total, %.1f interval\n",
        sim.VirtualTime(), sim.VirtualTime() - logger.lastLogTime)
    fmt.Printf("Flush(GB): cumulative %.3f, interval %.3f\n",
        sim.Metrics().TotalFlushWritten / 1024.0,
        /* interval flush */)
    fmt.Printf("Cumulative compaction: %.2f GB write, %.2f MB/s write, %.2f GB read, %.2f MB/s read, %.1f seconds\n",
        sim.Metrics().TotalDiskWritten / 1024.0,
        sim.Metrics().InstantaneousThroughputMBps,
        /* read stats */)
    fmt.Printf("Write Stall (count): l0-file-count-limit-delays: %d, l0-file-count-limit-stops: %d, memtable-limit-delays: %d, memtable-limit-stops: %d, total-delays: %d, total-stops: %d\n",
        /* stall counts from metrics */)

    logger.lastLogTime = sim.VirtualTime()
}

func formatSize(sizeMB float64) string {
    if sizeMB < 1.0 {
        return fmt.Sprintf("%.2f KB", sizeMB * 1024)
    } else if sizeMB < 1024.0 {
        return fmt.Sprintf("%.2f MB", sizeMB)
    } else {
        return fmt.Sprintf("%.2f GB", sizeMB / 1024)
    }
}

func formatKeys(keys int64) string {
    if keys < 1000 {
        return fmt.Sprintf("%d", keys)
    } else if keys < 1000000 {
        return fmt.Sprintf("%dK", keys / 1000)
    } else {
        return fmt.Sprintf("%dM", keys / 1000000)
    }
}
```

**RocksDB Reference:** `db/internal_stats.cc` lines 500-800
- `InternalStats::DumpCFStats()` - Generates compaction stats table

**Integration:**
```go
// In simulator.go
func (s *Simulator) Step() {
    // ... existing step logic ...

    // Log compaction stats periodically (every 10 min simulation time)
    if s.config.EnableRocksDBLogging && s.virtualTime - s.compactionStatsLogger.lastLogTime >= s.config.CompactionStatsIntervalSec {
        s.compactionStatsLogger.LogCompactionStats(s)
    }
}
```

#### 8.2 Individual Compaction Messages

**RocksDB Format** (from mork.log:1549):
```
2025/11/13-16:46:19.143041 37 [db/compaction/compaction_job.cc:908] [3] compacted to: base level 3 level multiplier 10.00 max bytes base 134217728 files[0 0 0 4 7 57 322] max score 12.48, MB/sec: 86.1 rd, 84.7 wr, level 3, files in(4, 3) out(3 +0 blob) MB in(54.6, 107.8 +0.0 blob) out(159.7 +0.0 blob), read-write-amplify(5.9) write-amplify(2.9) OK, records in: 4999823, records dropped: 79881 output_compression: NoCompression
```

**Go Implementation:**

```go
// In compactor.go
func (c *LeveledCompactor) logCompactionComplete(job *CompactionJob, lsm *LSMTree, config SimConfig, inputSize, outputSize float64, duration float64) {
    if !config.EnableRocksDBLogging {
        return
    }

    // Calculate stats
    baseLevel := lsm.calculateDynamicBaseLevel(config)
    levelMultiplier := config.LevelMultiplier
    maxBytesBase := config.MaxBytesForLevelBaseMB * 1024 * 1024 // Convert to bytes

    // Build files array (file count per level)
    filesPerLevel := make([]int, len(lsm.Levels))
    for i := range lsm.Levels {
        filesPerLevel[i] = lsm.Levels[i].FileCount
    }
    filesStr := fmt.Sprintf("[%s]", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(filesPerLevel)), " "), "[]"))

    // Calculate max score
    maxScore := 0.0
    for i := range lsm.Levels {
        if lsm.Levels[i].Score > maxScore {
            maxScore = lsm.Levels[i].Score
        }
    }

    // Calculate amplification
    readWriteAmplify := (inputSize + outputSize) / outputSize
    writeAmplify := outputSize / (outputSize / len(job.SourceFiles)) // Approximation

    // Calculate throughput
    readMBps := (inputSize / 1024) / duration  // Convert to GB/s
    writeMBps := (outputSize / 1024) / duration

    // Format log message
    timestamp := time.Now().Format("2006/01/02-15:04:05.000000")
    fmt.Printf("%s [simulator/compactor.go] compacted to: base level %d level multiplier %.2f max bytes base %d files%s max score %.2f, MB/sec: %.1f rd, %.1f wr, level %d, files in(%d, %d) out(%d +0 blob) MB in(%.1f, %.1f +0.0 blob) out(%.1f +0.0 blob), read-write-amplify(%.1f) write-amplify(%.1f) OK, records in: %d, records dropped: %d output_compression: %s\n",
        timestamp,
        baseLevel,
        float64(levelMultiplier),
        maxBytesBase,
        filesStr,
        maxScore,
        readMBps,
        writeMBps,
        job.ToLevel,
        len(job.SourceFiles),
        len(job.TargetFiles),
        /* output file count */,
        inputSize,
        /* target file size sum */,
        outputSize,
        readWriteAmplify,
        writeAmplify,
        /* estimated keys in (from file count * avg keys/file) */,
        /* estimated keys dropped */,
        config.CompressionAlgorithm)
}
```

**RocksDB Reference:** `db/compaction/compaction_job.cc` line 908
- `CompactionJob::LogCompaction()` - Generates individual compaction log

### Cost-Benefit Analysis

**High-Value (Must Do):**
- ✅ **Compaction Stats Table** - Critical for validation framework (Item #2)
  - Time: 4-6 hours
  - Benefit: Enables automated log comparison
  - Used by: Benchmark validation, production troubleshooting

**Medium-Value (Should Do):**
- ⚠️ **Individual Compaction Messages** - Nice for debugging, less critical
  - Time: 2-3 hours
  - Benefit: Easier visual comparison with RocksDB logs
  - Used by: Manual debugging, one-off comparisons

**Low-Value (Skip for Now):**
- ❌ **Other log messages** (flush, stall, etc.) - Not needed for validation
  - Time: 4-8 hours (many log types)
  - Benefit: Completeness, but diminishing returns
  - Decision: Skip unless specific need arises

**Recommendation:** Implement Compaction Stats Table (high-value) + Individual Compaction Messages (medium effort). Skip other log types.

#### Config Additions

```go
type SimConfig struct {
    // ... existing fields ...

    // Logging configuration
    EnableRocksDBLogging         bool    `json:"enableRocksDBLogging"`         // Enable RocksDB-style logs
    CompactionStatsIntervalSec   float64 `json:"compactionStatsIntervalSec"`   // Log interval (default 600 = 10 min)
}

func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing defaults ...
        EnableRocksDBLogging:       true,
        CompactionStatsIntervalSec: 600.0, // Match RocksDB's stats_dump_period_sec
    }
}
```

#### UI Components

**Config Panel:**
- ☑ Enable RocksDB-Style Logging
- 🎚️ Compaction Stats Interval: 60s - 3600s (default 600s)

**Log Display:**
- Text area showing RocksDB-formatted logs
- Download button (save to file)
- Clear button

#### Time Estimate
**6-9 hours**
- 4-6 hours: Compaction Stats Table implementation
- 2-3 hours: Individual compaction messages
- Total: High-value logging for validation

---

## 9. Benchmark Validation Framework

### Goal

**Measure real-world fidelity** by comparing simulator against actual RocksDB behavior on identical workloads.

### Strategy

1. **Run real RocksDB benchmark** (db_bench or YCSB)
2. **Capture RocksDB logs** (compaction stats, DB stats)
3. **Recreate workload in simulator** (same config, same traffic pattern)
4. **Compare logs side-by-side** (automated diff)
5. **Quantify fidelity** (% difference in key metrics)

### Implementation

#### 9.1 Benchmark Selection

**Option A: RocksDB db_bench (Recommended)**
- Built-in benchmark tool
- Well-documented workloads
- Easy to run and reproduce
- Logs have consistent format

**Option B: YCSB (Yahoo! Cloud Serving Benchmark)**
- Industry-standard benchmark
- More realistic workloads
- Harder to integrate (requires Java)
- Less structured logs

**Recommendation:** Start with db_bench (simpler), add YCSB later if needed.

#### 9.2 Benchmark Workloads to Test

**Priority 1 (Must Test):**
1. **fillseq** - Sequential writes (pure write workload)
2. **fillrandom** - Random writes (write amplification test)
3. **readrandom** - Point lookups (read amplification test)
4. **readwhilewriting** - Mixed workload (50% read, 50% write)

**Priority 2 (Nice to Test):**
5. **overwrite** - Update existing keys (compaction effectiveness)
6. **readseq** - Sequential scans (range query test)
7. **readreverse** - Reverse scans

#### 9.3 Automated Benchmark Runner

**Go Implementation:**

```go
// In benchmark/runner.go
package benchmark

import (
    "fmt"
    "os/exec"
    "github.com/yevgeniy-miretskiy/rollingstone/simulator"
)

type BenchmarkRunner struct {
    rocksDBPath    string  // Path to db_bench binary
    workDir        string  // Working directory for DB files
    logDir         string  // Directory for log output
}

type BenchmarkConfig struct {
    Workload           string  // "fillseq", "fillrandom", "readrandom", etc.
    NumKeys            int     // Number of keys to write
    ValueSize          int     // Size of each value (bytes)
    WriteBufferSize    int     // memtable size
    TargetFileSize     int     // SST file size
    MaxBytesForLevelBase int   // L1 size
    LevelMultiplier    int     // Level size multiplier
    Compression        string  // "snappy", "zstd", etc.
    NumThreads         int     // Concurrency level
}

func (r *BenchmarkRunner) RunRocksDB(config BenchmarkConfig) (*BenchmarkResult, error) {
    // Build db_bench command
    cmd := exec.Command(r.rocksDBPath,
        fmt.Sprintf("--benchmarks=%s", config.Workload),
        fmt.Sprintf("--num=%d", config.NumKeys),
        fmt.Sprintf("--value_size=%d", config.ValueSize),
        fmt.Sprintf("--write_buffer_size=%d", config.WriteBufferSize),
        fmt.Sprintf("--target_file_size_base=%d", config.TargetFileSize),
        fmt.Sprintf("--max_bytes_for_level_base=%d", config.MaxBytesForLevelBase),
        fmt.Sprintf("--max_bytes_for_level_multiplier=%d", config.LevelMultiplier),
        fmt.Sprintf("--compression_type=%s", config.Compression),
        fmt.Sprintf("--threads=%d", config.NumThreads),
        fmt.Sprintf("--db=%s/rocksdb_bench", r.workDir),
        fmt.Sprintf("--statistics"),
        fmt.Sprintf("--stats_interval_seconds=60"),
        "--use_existing_db=0",
        "--disable_wal=0")

    // Capture output
    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("db_bench failed: %v", err)
    }

    // Parse logs
    result := parseRocksDBLogs(string(output))
    return result, nil
}

func (r *BenchmarkRunner) RunSimulator(config BenchmarkConfig) (*BenchmarkResult, error) {
    // Convert BenchmarkConfig to SimConfig
    simConfig := simulator.DefaultConfig()
    simConfig.WriteRateMBps = float64(config.ValueSize * 1000) / (1024 * 1024) // Approximate
    simConfig.MemtableFlushSizeMB = float64(config.WriteBufferSize) / (1024 * 1024)
    simConfig.TargetFileSizeMB = float64(config.TargetFileSize) / (1024 * 1024)
    simConfig.MaxBytesForLevelBaseMB = float64(config.MaxBytesForLevelBase) / (1024 * 1024)
    simConfig.LevelMultiplier = config.LevelMultiplier
    simConfig.CompressionAlgorithm = config.Compression
    simConfig.EnableRocksDBLogging = true

    // Run simulator
    sim, err := simulator.NewSimulator(simConfig)
    if err != nil {
        return nil, err
    }

    // Estimate run time (keys * value_size / write_rate)
    estimatedTime := float64(config.NumKeys * config.ValueSize) / (simConfig.WriteRateMBps * 1024 * 1024)
    sim.Run(estimatedTime)

    // Extract metrics
    result := &BenchmarkResult{
        WriteAmplification: sim.Metrics().WriteAmplification,
        SpaceAmplification: sim.Metrics().SpaceAmplification,
        ThroughputMBps:     sim.Metrics().InstantaneousThroughputMBps,
        // ... other metrics ...
    }

    return result, nil
}

type BenchmarkResult struct {
    WriteAmplification float64
    SpaceAmplification float64
    ReadAmplification  float64
    ThroughputMBps     float64
    CompactionCount    int
    StallCount         int

    // Per-level stats (from compaction stats table)
    LevelStats map[int]LevelStats
}

type LevelStats struct {
    FileCount       int
    SizeMB          float64
    CompactionCount int
    BytesRead       float64
    BytesWritten    float64
}

func (r *BenchmarkRunner) Compare(rocksDBResult, simulatorResult *BenchmarkResult) *ComparisonReport {
    report := &ComparisonReport{}

    // Calculate deltas
    report.WriteAmpDelta = percentDelta(simulatorResult.WriteAmplification, rocksDBResult.WriteAmplification)
    report.SpaceAmpDelta = percentDelta(simulatorResult.SpaceAmplification, rocksDBResult.SpaceAmplification)
    report.ThroughputDelta = percentDelta(simulatorResult.ThroughputMBps, rocksDBResult.ThroughputMBps)

    // Per-level comparison
    for level := range rocksDBResult.LevelStats {
        simStats := simulatorResult.LevelStats[level]
        rocksStats := rocksDBResult.LevelStats[level]

        report.LevelDeltas[level] = LevelDelta{
            FileCountDelta: percentDelta(float64(simStats.FileCount), float64(rocksStats.FileCount)),
            SizeDelta:      percentDelta(simStats.SizeMB, rocksStats.SizeMB),
        }
    }

    // Overall fidelity score
    report.FidelityScore = calculateFidelityScore(report)

    return report
}

type ComparisonReport struct {
    WriteAmpDelta   float64 // % difference
    SpaceAmpDelta   float64
    ThroughputDelta float64
    LevelDeltas     map[int]LevelDelta
    FidelityScore   float64 // 0-100
}

func percentDelta(simValue, rocksValue float64) float64 {
    if rocksValue == 0 {
        return 0
    }
    return ((simValue - rocksValue) / rocksValue) * 100.0
}

func calculateFidelityScore(report *ComparisonReport) float64 {
    // Weighted fidelity score
    // Weight important metrics higher
    waWeight := 0.3
    saWeight := 0.3
    tpWeight := 0.2
    levelWeight := 0.2

    // Convert deltas to accuracy (100% - abs(delta))
    waAccuracy := 100.0 - math.Abs(report.WriteAmpDelta)
    saAccuracy := 100.0 - math.Abs(report.SpaceAmpDelta)
    tpAccuracy := 100.0 - math.Abs(report.ThroughputDelta)

    // Average level accuracy
    levelAccuracy := 0.0
    for _, delta := range report.LevelDeltas {
        levelAccuracy += 100.0 - math.Abs(delta.SizeDelta)
    }
    if len(report.LevelDeltas) > 0 {
        levelAccuracy /= float64(len(report.LevelDeltas))
    }

    // Weighted score
    score := waWeight*waAccuracy + saWeight*saAccuracy + tpWeight*tpAccuracy + levelWeight*levelAccuracy
    return math.Max(0, math.Min(100, score))
}
```

**RocksDB Reference:**
- db_bench source: `tools/db_bench.cc`
- Statistics: `monitoring/statistics.h`

#### 9.4 Traffic Pattern Recreation

**Challenge:** db_bench write rate varies (bursty writes, compaction pauses, stalls).

**Solution 1 (Simple):** Use average write rate
```go
// Calculate average write rate from RocksDB logs
totalBytes := /* from logs */
totalTime := /* from logs */
avgRateMBps := totalBytes / totalTime

simConfig.WriteRateMBps = avgRateMBps
```

**Solution 2 (Advanced):** Replay actual write pattern
```go
// Parse db_bench logs for write timestamps
type WriteEvent struct {
    Timestamp float64
    SizeMB    float64
}

func parseWritePattern(logs string) []WriteEvent {
    // Extract writes from logs (flushed memtables)
    // Return timeline of writes
}

// Replay in simulator
for _, event := range writePattern {
    sim.ScheduleWrite(event.Timestamp, event.SizeMB)
}
```

**Recommendation:** Start with Solution 1 (simple), add Solution 2 if deltas are large.

#### 9.5 Automated Test Suite

**Go Test:**

```go
// In benchmark/validation_test.go
func TestFidelity_Fillseq(t *testing.T) {
    runner := NewBenchmarkRunner("/path/to/db_bench", "/tmp/bench", "/tmp/logs")

    config := BenchmarkConfig{
        Workload:           "fillseq",
        NumKeys:            10000000, // 10M keys
        ValueSize:          1000,     // 1KB values
        WriteBufferSize:    64 * 1024 * 1024,
        TargetFileSize:     64 * 1024 * 1024,
        MaxBytesForLevelBase: 256 * 1024 * 1024,
        LevelMultiplier:    10,
        Compression:        "snappy",
        NumThreads:         1,
    }

    // Run both
    rocksResult, err := runner.RunRocksDB(config)
    require.NoError(t, err)

    simResult, err := runner.RunSimulator(config)
    require.NoError(t, err)

    // Compare
    report := runner.Compare(rocksResult, simResult)

    // Assertions (Level B thresholds)
    assert.Less(t, math.Abs(report.WriteAmpDelta), 5.0, "Write amp within 5%")
    assert.Less(t, math.Abs(report.SpaceAmpDelta), 10.0, "Space amp within 10%")
    assert.Less(t, math.Abs(report.ThroughputDelta), 15.0, "Throughput within 15%")

    assert.Greater(t, report.FidelityScore, 90.0, "Overall fidelity > 90%")

    // Print report
    t.Logf("Fidelity Report for fillseq:")
    t.Logf("  Write Amplification: %.2f (RocksDB) vs %.2f (Simulator) = %.1f%% delta",
        rocksResult.WriteAmplification, simResult.WriteAmplification, report.WriteAmpDelta)
    t.Logf("  Space Amplification: %.2f vs %.2f = %.1f%% delta",
        rocksResult.SpaceAmplification, simResult.SpaceAmplification, report.SpaceAmpDelta)
    t.Logf("  Throughput: %.1f MB/s vs %.1f MB/s = %.1f%% delta",
        rocksResult.ThroughputMBps, simResult.ThroughputMBps, report.ThroughputDelta)
    t.Logf("  Overall Fidelity Score: %.1f/100", report.FidelityScore)
}

// Repeat for other workloads
func TestFidelity_Fillrandom(t *testing.T) { /* ... */ }
func TestFidelity_Readrandom(t *testing.T) { /* ... */ }
func TestFidelity_ReadWhileWriting(t *testing.T) { /* ... */ }
```

#### CI/CD Integration

**GitHub Actions:**
```yaml
name: Fidelity Validation

on: [push, pull_request]

jobs:
  validate-fidelity:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2

      - name: Install RocksDB
        run: |
          git clone https://github.com/facebook/rocksdb.git
          cd rocksdb
          make db_bench

      - name: Run Fidelity Tests
        run: |
          go test -v ./benchmark -run TestFidelity

      - name: Upload Reports
        uses: actions/upload-artifact@v2
        with:
          name: fidelity-reports
          path: /tmp/bench/reports/
```

#### Time Estimate
**8-12 hours**
- 4 hours: Benchmark runner framework
- 2 hours: Log parsing (RocksDB compaction stats)
- 2 hours: Traffic pattern recreation
- 2 hours: Comparison logic + reporting
- 2 hours: Test suite + CI integration

#### Deliverables

1. **Automated benchmark runner** (runs db_bench + simulator)
2. **Log comparison tool** (side-by-side diff)
3. **Fidelity report** (% deltas, overall score)
4. **Test suite** (4 workloads: fillseq, fillrandom, readrandom, readwhilewriting)
5. **CI integration** (run on every commit)

---

## Implementation Timeline

### Week 1: Write Path Extensions

**Day 1: WAL (4-6 hours)**
- Implement WAL write events
- Integrate with write path
- Add config + UI
- Test write amp increase

**Day 2: CPU Modeling (6-8 hours)**
- Implement CPU tracker
- Add compression throughput table
- Integrate with compaction execution
- Add config + UI
- Test CPU vs I/O bottleneck

**Day 3: Write Controller (4-6 hours)**
- Implement credit-based rate limiter
- Integrate with existing stall logic
- Add soft delay thresholds
- Add config + UI
- Test gradual throttling

**Day 4: Compression + Testing (4-6 hours)**
- Clarify compression vs deduplication
- Update compaction execution
- Run integration tests
- Fix any bugs

**Checkpoint:** Write path fidelity = 85%

### Week 2: Read Path

**Day 5-6: Read Path Foundation (8-10 hours)**
- Implement read events
- Add point lookup logic (memtable → L0 → L1+)
- Add range scan logic (future)
- Integrate with LSM structure
- Add config + UI
- Test read amplification

**Day 7: Bloom Filters + Block Cache (6-8 hours)**
- Implement probabilistic bloom filters
- Implement LRU block cache
- Integrate with read path
- Add config + UI
- Test bloom filter effectiveness
- Test cache hit rates

**Day 8: Testing & Documentation (4-6 hours)**
- Run full test suite
- Compare with RocksDB db_bench
- Document limitations
- Update README

**Checkpoint:** Overall fidelity = 90% (Level B achieved)

---

## Validation Plan

### Benchmarks to Run

**RocksDB db_bench:**
1. `fillseq` - Sequential writes
2. `fillrandom` - Random writes
3. `readrandom` - Point lookups
4. `readwhilewriting` - Mixed workload

### Metrics to Compare

| Metric | Simulator | RocksDB | Threshold |
|--------|-----------|---------|-----------|
| Write Amplification | X.XX | X.XX | ±5% |
| Space Amplification | X.XX | X.XX | ±10% |
| Read Amplification | X.XX | X.XX | ±25% |
| Throughput (MB/s) | XXX | XXX | ±15% |
| Stall Rate (%) | XX | XX | ±20% |
| Bloom Filter Hit Rate | XX% | XX% | ±10% |
| Block Cache Hit Rate | XX% | XX% | ±15% |

### Success Criteria for Level B

- ✅ Write amp within 5% of RocksDB
- ✅ Space amp within 10% of RocksDB
- ✅ Read amp within 25% of RocksDB (bloom filters have inherent randomness)
- ✅ Throughput within 15% of RocksDB
- ✅ All test benchmarks pass

---

## Level A Features (Stretch Goal)

After achieving Level B, consider these additional features for 95% fidelity:

### 1. TTL / Periodic Compaction (2-3 days)
- Implement time-based compaction triggers
- Support `periodic_compaction_seconds` config
- **Impact:** +2% fidelity for TTL workloads

### 2. Multi-threaded CPU Contention (3-4 days)
- Model CPU scheduler with multiple cores
- Operations compete for CPU time
- **Impact:** +2% fidelity for multi-job workloads

### 3. Compression Variance (1-2 days)
- Add per-file compression ratio variance
- Calibrate from real RocksDB data
- **Impact:** +1% fidelity

**Total Time for Level A:** +1-2 weeks
**Final Fidelity:** 95%

---

## Summary

**Current State:** C+ (70-75%)
**Level B Target:** 90% (1-2 weeks)
**Level A Target:** 95% (2-3 weeks)

**Key Priorities:**
1. WAL - Critical for write amp accuracy
2. CPU Modeling - Critical for throughput accuracy
3. Read Path - Critical for mixed workload support
4. Bloom Filters - Critical for read amp accuracy
5. Block Cache - Critical for read latency accuracy

**Architecture:**
- Library-first design (Go code usable standalone)
- UI is visualization layer only
- Config in Go structs, not UI
- Agents can import and run simulations programmatically

**Validation:**
- Compare all metrics vs RocksDB db_bench
- Document correction factors and limitations
- Achieve 90% fidelity certification
