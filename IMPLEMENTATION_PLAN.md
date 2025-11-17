# Implementation Plan: Compression & Read Path Modeling

**Date:** 2025-11-16
**Based on:** Production analysis, web research, and RocksDB source verification

---

## Phase 1: Compression Throughput Modeling (HIGH PRIORITY - DO NOW)

### Research Findings

#### Compression Speed Benchmarks (Web Search Results)

From lzbench and RocksDB wiki:

| Algorithm | Compression (MB/s) | Decompression (MB/s) | Compression Ratio | Notes |
|-----------|-------------------|---------------------|-------------------|-------|
| **LZ4** | **750** | **3700** | 2.10 | RocksDB recommended (fast, good ratio) |
| **Snappy** | **530** | **1800** | 2.09 | RocksDB default, but LZ4 is better |
| **Zstd (default)** | **470** | **1380** | 2.88 | Best ratio, still fast |
| **Zstd-3** | ~635 | ~1980 | ~2.5 | Faster Zstd variant |
| **None** | ∞ | ∞ | 1.00 | No CPU cost |

**Key Insights:**
- Previous estimates (2000/3000 MB/s) were **too high** (based on "wire speed" marketing)
- Real-world single-threaded: **LZ4 ~750 MB/s compression, 3700 MB/s decompression**
- Snappy is slightly slower than LZ4 in all metrics (RocksDB devs recommend LZ4 over Snappy)
- Zstd provides 37% better compression ratio but is slower

#### RocksDB Block Size Verification

**Source:** `~/src/rocksdb/include/rocksdb/table.h:186`

```cpp
// Approximate size of user data packed per block. Note that the
// block size specified here corresponds to uncompressed data.
uint64_t block_size = 4 * 1024;  // 4 KB DEFAULT
```

**Finding:** RocksDB default is **4 KB**, not 16 KB as I previously stated.

**Impact on Compression:**
- Smaller blocks (4 KB) → **worse compression ratio** (less context for compressor)
- Typical compression ratios assume much larger blocks (64 KB - 1 MB)
- With 4 KB blocks, compression factor might be closer to **0.85-0.90** instead of 0.7

---

### Corrected Duration Calculation

**You're absolutely right** - compression duration is **additive**, not max():

```
Compaction Process:
1. Read input SST files (I/O)
2. Decompress blocks (CPU)
3. Merge/sort keys (CPU - negligible)
4. Compress output blocks (CPU)
5. Write output SST files (I/O)

Duration = Read I/O + Decompress + Compress + Write I/O
```

**Corrected Formula:**
```go
func (s *Simulator) calculateCompactionDuration(inputMB, outputMB float64) float64 {
    // Read input files
    readIOTime := inputMB / s.config.IOThroughputMBps

    // Decompress input data
    decompressTime := inputMB / s.config.DecompressionThroughputMBps

    // Compress output data
    compressTime := outputMB / s.config.CompressionThroughputMBps

    // Write output files
    writeIOTime := outputMB / s.config.IOThroughputMBps

    // All operations are sequential (additive)
    return readIOTime + decompressTime + compressTime + writeIOTime
}
```

**Example (using real benchmarks):**
```
Compact 128 MB input → 90 MB output (0.7 compression factor)
I/O: 500 MB/s, Compression: 750 MB/s, Decompression: 3700 MB/s

Read I/O:     128 / 500  = 256 ms
Decompress:   128 / 3700 = 35 ms
Compress:     90  / 750  = 120 ms
Write I/O:    90  / 500  = 180 ms
─────────────────────────────────
TOTAL:                      591 ms

Compare to I/O-only model:
(128 + 90) / 500 = 436 ms

Overhead: 591 / 436 = 1.36x (36% slower due to compression)
```

**This matches your 12.5% CPU observation better:**
- Compression: 120 ms
- Decompression: 35 ms
- Total CPU: 155 ms
- Total Duration: 591 ms
- **CPU %: 155 / 591 = 26.2%** (higher than 12.5% because I/O is slower in this example)

---

### Implementation

#### 1. Add Configuration Parameters

```go
// In simulator/config.go
type SimConfig struct {
    // ... existing fields ...

    // Compression configuration
    CompressionFactor           float64 `json:"compressionFactor"`           // Physical size reduction (existing)
    CompressionThroughputMBps   float64 `json:"compressionThroughputMBps"`   // NEW: CPU throughput
    DecompressionThroughputMBps float64 `json:"decompressionThroughputMBps"` // NEW: CPU throughput
    BlockSizeKB                 int     `json:"blockSizeKB"`                 // NEW: For compression efficiency
}

// Updated defaults
func DefaultConfig() SimConfig {
    return SimConfig{
        // ... existing ...

        // Compression defaults (LZ4 - RocksDB recommended)
        CompressionFactor:           0.7,    // 30% reduction (optimistic for 4KB blocks)
        CompressionThroughputMBps:   750,    // LZ4 single-threaded
        DecompressionThroughputMBps: 3700,   // LZ4 single-threaded
        BlockSizeKB:                 4,      // RocksDB default (verified)
    }
}
```

#### 2. Compression Presets

```go
// Compression algorithm presets
type CompressionPreset struct {
    Name            string
    Factor          float64  // Compression ratio (smaller = better compression)
    CompressMBps    float64  // Single-threaded compression speed
    DecompressMBps  float64  // Single-threaded decompression speed
    Description     string
}

var CompressionPresets = map[string]CompressionPreset{
    "none": {
        Name:            "None",
        Factor:          1.0,
        CompressMBps:    math.Inf(1),  // No CPU cost
        DecompressMBps:  math.Inf(1),
        Description:     "No compression (fastest, largest files)",
    },
    "lz4": {
        Name:            "LZ4",
        Factor:          0.7,   // Assumes larger blocks; may be 0.85 with 4KB blocks
        CompressMBps:    750,   // From benchmarks
        DecompressMBps:  3700,  // From benchmarks
        Description:     "LZ4 (RocksDB recommended: fast, good compression)",
    },
    "snappy": {
        Name:            "Snappy",
        Factor:          0.7,   // Similar to LZ4
        CompressMBps:    530,   // From benchmarks (slower than LZ4)
        DecompressMBps:  1800,  // From benchmarks (2x slower than LZ4)
        Description:     "Snappy (RocksDB default, but LZ4 is better)",
    },
    "zstd": {
        Name:            "Zstd (default)",
        Factor:          0.5,   // 50% reduction (better than LZ4)
        CompressMBps:    470,   // From benchmarks
        DecompressMBps:  1380,  // From benchmarks
        Description:     "Zstd default (best compression, moderate speed)",
    },
    "zstd-fast": {
        Name:            "Zstd (fast)",
        Factor:          0.6,   // 40% reduction
        CompressMBps:    635,   // From benchmarks
        DecompressMBps:  1980,  // From benchmarks
        Description:     "Zstd fast mode (good balance of speed and compression)",
    },
}

// Helper to apply preset
func (c *SimConfig) ApplyCompressionPreset(presetName string) {
    preset, ok := CompressionPresets[presetName]
    if !ok {
        return  // Unknown preset, keep current values
    }

    c.CompressionFactor = preset.Factor
    c.CompressionThroughputMBps = preset.CompressMBps
    c.DecompressionThroughputMBps = preset.DecompressMBps
}
```

#### 3. Update Compaction Duration Calculation

```go
// In simulator/simulator.go (update existing function)
func (s *Simulator) scheduleCompactionCompletion(
    event *CompactionStartEvent,
    inputSizeMB float64,
    outputSizeMB float64,
) {
    // Read input files (I/O)
    readIOTime := inputSizeMB / s.config.IOThroughputMBps

    // Decompress input data (CPU)
    decompressTime := inputSizeMB / s.config.DecompressionThroughputMBps

    // Compress output data (CPU)
    compressTime := outputSizeMB / s.config.CompressionThroughputMBps

    // Write output files (I/O)
    writeIOTime := outputSizeMB / s.config.IOThroughputMBps

    // Sequential operations (additive)
    duration := readIOTime + decompressTime + compressTime + writeIOTime

    // Schedule completion
    completionTime := s.virtualTime + duration
    s.queue.Push(NewCompactionCompleteEvent(completionTime, event.Level, /* ... */))
}
```

#### 4. Update Flush Duration (Similar Logic)

```go
// Flush: Compress memtable and write to L0
func (s *Simulator) scheduleFlushCompletion(sizeMB float64) {
    // Compress data (CPU)
    compressTime := sizeMB / s.config.CompressionThroughputMBps

    // Write to disk (I/O)
    // Note: Output size is reduced by compression factor
    outputSizeMB := sizeMB * s.config.CompressionFactor
    writeIOTime := outputSizeMB / s.config.IOThroughputMBps

    duration := compressTime + writeIOTime

    completionTime := s.virtualTime + duration
    s.queue.Push(NewFlushCompleteEvent(completionTime, /* ... */))
}
```

---

### Block Size Impact on Compression

**Note:** With 4 KB blocks (RocksDB default), compression efficiency is **lower** than with larger blocks.

**Compression Factor Adjustments:**

| Block Size | LZ4 Factor | Snappy Factor | Zstd Factor | Notes |
|------------|------------|---------------|-------------|-------|
| 4 KB       | **0.85**   | **0.85**      | **0.70**    | Limited context, poor compression |
| 16 KB      | **0.75**   | **0.75**      | **0.60**    | Better compression |
| 64 KB      | **0.70**   | **0.70**      | **0.50**    | Good compression (benchmark typical) |

**Recommendation:** Document this in configuration comments:

```go
// CompressionFactor represents physical size reduction after compression.
// Typical values:
//   - 0.85-0.90 with 4KB blocks (RocksDB default, limited compression)
//   - 0.70-0.75 with 16KB blocks
//   - 0.60-0.70 with 64KB blocks (benchmark typical)
// Smaller values = better compression (smaller output files).
CompressionFactor float64 `json:"compressionFactor"` // Default: 0.85 (conservative for 4KB blocks)
```

**Updated Default:**
```go
CompressionFactor: 0.85,  // Changed from 0.7 (more realistic for 4KB blocks)
```

---

## Phase 2: Configuration Presets (DEFERRED - Documentation Only)

**Agreed:** Skip implementation, document presets instead.

Create `WORKLOAD_PRESETS.md`:

```markdown
# Workload Presets for RocksDB Simulator

## Mork Production (Read-Heavy)
- **Target File Size:** 134 MB
- **Max Bytes for Level Base:** 1024 MB (1 GB)
- **Level Multiplier:** 4.0
- **Compression:** LZ4 (750 MB/s compress, 3700 MB/s decompress)
- **Block Size:** 4 KB
- **Cache Hit Rate:** 98.8% (from production metrics)

## Dist-Aggr Production (Write-Heavy)
- **Target File Size:** 64 MB (unknown, using default)
- **Compression:** Snappy (530 MB/s compress, 1800 MB/s decompress)
- **Write Amplification:** 1.16x (validated)

## AWS m5d.2xlarge Benchmark
- **I/O Throughput:** 500 MB/s
- **Compression:** Snappy (530 MB/s compress, 1800 MB/s decompress)
- **Target File Size:** 64 MB
```

**BUT:** Keep block size as configurable parameter (it's interesting for optimization).

---

## Phase 3: Read Path Modeling (IMPLEMENT - High Value for Evolutionary Algorithm)

### Your Excellent Point: LSM State Affects Read Latency

**Key Insight:** Read latency depends on **read amplification**, which depends on **LSM structure** (number of files, levels).

**Example:**
```
Cache miss on point lookup:
- Bloom filter checks: L0 (8 files) + L1 (1 file) + L2 (1 file) = 10 checks
- If Bloom filter false positive (1%): Read block from disk
- Read amplification = 10 blocks read (worst case)

Total latency = 10 * (I/O + decompress)
             = 10 * (1ms + 0.01ms)
             = 10.1 ms

vs. good LSM with 3 levels:
Read amplification = 3 blocks
Total latency = 3 * 1.01 = 3.03 ms
```

**This is CRITICAL for evolutionary algorithm** - bad LSM structure significantly degrades read performance!

### Simplified Read Path Implementation

**No need to model everything** - just model the key factors:

```go
// Read request characteristics (distribution-based, not discrete events)
type ReadWorkload struct {
    RequestsPerSecond   float64  // Total read rate

    // Request type distribution (must sum to 1.0)
    CacheHitRate        float64  // % that hit block cache (0.90 = 90%)
    BloomNegativeRate   float64  // % that are negative lookups (0.02 = 2%)
    ScanRate            float64  // % that are range scans (0.08 = 8%)
    // Remaining % are point lookups with cache miss

    // Scan characteristics
    AvgScanSizeKB       float64  // Average scan size in KB (for sequential reads)
}

// Calculate average read latency based on current LSM state
func (s *Simulator) calculateReadLatency(workload ReadWorkload) float64 {
    var totalLatency float64

    // 1. Cache hits (fastest)
    cacheHitLatency := s.calculateCacheHitLatency()
    totalLatency += workload.CacheHitRate * cacheHitLatency

    // 2. Bloom filter negative lookups (fast, just Bloom filter checks)
    bloomLatency := s.calculateBloomFilterLatency()
    totalLatency += workload.BloomNegativeRate * bloomLatency

    // 3. Point lookups with cache miss (expensive, depends on LSM structure)
    pointLookupLatency := s.calculatePointLookupLatency()
    cacheMissRate := 1.0 - workload.CacheHitRate - workload.BloomNegativeRate - workload.ScanRate
    totalLatency += cacheMissRate * pointLookupLatency

    // 4. Range scans (very expensive, sequential I/O)
    scanLatency := s.calculateScanLatency(workload.AvgScanSizeKB)
    totalLatency += workload.ScanRate * scanLatency

    return totalLatency
}

// Cache hit: just decompression (if cache stores compressed blocks)
func (s *Simulator) calculateCacheHitLatency() float64 {
    if s.config.BlockCacheCompressed {
        // Must decompress from cache
        blockSizeMB := float64(s.config.BlockSizeKB) / 1024.0
        return blockSizeMB / s.config.DecompressionThroughputMBps * 1000  // Convert to ms
    }
    // Uncompressed cache: memory access only
    return 0.001  // 1 microsecond
}

// Bloom filter negative lookup: check all levels' Bloom filters
func (s *Simulator) calculateBloomFilterLatency() float64 {
    // Bloom filter check is very fast (nanoseconds), but we check multiple levels
    numLevels := len(s.lsm.Levels)
    numL0Files := len(s.lsm.L0Files)

    // L0: check each file's Bloom filter
    // L1+: check one merged Bloom filter per level
    totalChecks := numL0Files + numLevels

    // Each Bloom filter check: ~100 nanoseconds (0.0001 ms)
    return float64(totalChecks) * 0.0001
}

// Point lookup with cache miss: read amplification matters!
func (s *Simulator) calculatePointLookupLatency() float64 {
    // Calculate read amplification (how many blocks we must read)
    readAmp := s.calculateReadAmplification()

    // Each block read: I/O + decompression
    blockSizeMB := float64(s.config.BlockSizeKB) / 1024.0
    ioLatency := s.config.IOLatencyMs + (blockSizeMB / s.config.IOThroughputMBps * 1000)
    decompressLatency := blockSizeMB / s.config.DecompressionThroughputMBps * 1000

    perBlockLatency := ioLatency + decompressLatency

    // Total latency = read amplification * per-block latency
    return readAmp * perBlockLatency
}

// Read amplification: depends on LSM structure (key insight!)
func (s *Simulator) calculateReadAmplification() float64 {
    numL0Files := float64(len(s.lsm.L0Files))
    numLevels := float64(len(s.lsm.Levels))

    // Worst case: check all L0 files + one file per level
    // Bloom filter reduces this by (1 - false_positive_rate)
    bloomFPR := 0.01  // 1% false positive rate (typical)

    // Expected reads = L0 files * FPR + levels * FPR
    expectedReads := (numL0Files + numLevels) * bloomFPR

    // But must read at least 1 block (the actual key)
    return math.Max(1.0, expectedReads)
}

// Range scan: sequential I/O across multiple blocks
func (s *Simulator) calculateScanLatency(scanSizeKB float64) float64 {
    scanSizeMB := scanSizeKB / 1024.0

    // Sequential I/O (faster than random)
    sequentialThroughput := s.config.IOThroughputMBps * 1.5  // 50% faster for sequential
    ioLatency := (scanSizeMB / sequentialThroughput) * 1000  // ms

    // Decompression (must decompress all blocks in range)
    decompressLatency := (scanSizeMB / s.config.DecompressionThroughputMBps) * 1000  // ms

    return ioLatency + decompressLatency
}
```

### Integration with Simulator

**Option 1: Metrics-Only (Simplest)**
```go
// Add to simulator metrics
type Metrics struct {
    // ... existing ...

    // Read path metrics
    AvgReadLatencyMs    float64  // Calculated based on LSM state
    ReadAmplification   float64  // Current read amplification factor
}

// Update after each compaction/flush
func (s *Simulator) updateReadMetrics(workload ReadWorkload) {
    s.metrics.AvgReadLatencyMs = s.calculateReadLatency(workload)
    s.metrics.ReadAmplification = s.calculateReadAmplification()
}
```

**Option 2: Discrete Events (More Accurate)**
```go
// Add ReadEvent type
type ReadEvent struct {
    EventBase
    RequestType  string  // "point_lookup", "scan", "cache_hit", "bloom_negative"
}

// Process read events
func (s *Simulator) processReadEvent(event *ReadEvent) {
    latency := 0.0

    switch event.RequestType {
    case "cache_hit":
        latency = s.calculateCacheHitLatency()
    case "bloom_negative":
        latency = s.calculateBloomFilterLatency()
    case "point_lookup":
        latency = s.calculatePointLookupLatency()
    case "scan":
        latency = s.calculateScanLatency(16.0)  // 16 KB default
    }

    // Schedule read completion
    completionTime := s.virtualTime + latency / 1000.0  // Convert ms to seconds
    s.queue.Push(NewReadCompleteEvent(completionTime))
}
```

### Configuration

```go
type SimConfig struct {
    // ... existing ...

    // Read path configuration
    BlockSizeKB          int     `json:"blockSizeKB"`          // Default: 4 (RocksDB default)
    BlockCacheCompressed bool    `json:"blockCacheCompressed"` // Default: false
    IOLatencyMs          float64 `json:"ioLatencyMs"`          // Default: 1.0 ms (SSD)
}
```

---

## Summary: What to Implement

### Phase 1 (DO NOW - 4-6 hours):
1. ✅ Add `CompressionThroughputMBps` and `DecompressionThroughputMBps` parameters
2. ✅ Create compression presets (LZ4, Snappy, Zstd, None)
3. ✅ Update compaction duration: **additive model** (read + decompress + compress + write)
4. ✅ Update flush duration: compress + write
5. ✅ Add `BlockSizeKB` parameter (default 4 KB, verified from RocksDB source)
6. ✅ Adjust default `CompressionFactor` to 0.85 (realistic for 4KB blocks)
7. ✅ Update benchmarks with real-world speeds (LZ4: 750/3700 MB/s)

### Phase 2 (DEFER - Documentation only):
- Create `WORKLOAD_PRESETS.md` with Mork, dist-aggr, AWS presets

### Phase 3 (DO AFTER PHASE 1 - 6-8 hours):
1. ✅ Add read latency calculation functions (cache hit, Bloom filter, point lookup, scan)
2. ✅ Add read amplification calculation (depends on LSM structure - **KEY INSIGHT**)
3. ✅ Add `ReadWorkload` configuration (distribution-based)
4. ✅ Integrate read metrics into simulator (either metrics-only or discrete events)
5. ✅ Add `IOLatencyMs` parameter for SSD seek time

---

## Testing Plan

### Phase 1 Validation:
```go
// Test: Compression adds overhead
config := DefaultConfig()
config.CompressionThroughputMBps = 750
config.DecompressionThroughputMBps = 3700

inputMB := 128.0
outputMB := 90.0

// OLD model (I/O only): (128 + 90) / 500 = 436 ms
// NEW model (I/O + CPU): 256 + 35 + 120 + 180 = 591 ms
// Expected overhead: 591 / 436 = 1.36x

duration := sim.calculateCompactionDuration(inputMB, outputMB)
assert(duration > 0.5, "Duration should be > 500ms")
assert(duration < 0.7, "Duration should be < 700ms")
```

### Phase 3 Validation:
```go
// Test: Bad LSM structure increases read latency
goodLSM := createLSM(numL0Files: 2, numLevels: 3)
badLSM := createLSM(numL0Files: 20, numLevels: 6)

goodLatency := sim.calculatePointLookupLatency(goodLSM)
badLatency := sim.calculatePointLookupLatency(badLSM)

assert(badLatency > goodLatency * 2, "Bad LSM should have 2x+ latency")
```

---

## Documentation Updates

1. Update `CLAUDE.md` with compression modeling details
2. Create `WORKLOAD_PRESETS.md` with production configs
3. Update `benchmarks/README.md` with real compression speeds
4. Add comments in `config.go` explaining block size impact on compression

---

## Conclusion

**Phase 1** addresses the 12.5% CPU overhead with simple, accurate modeling. The **additive duration model** you suggested is correct and matches real compaction behavior.

**Phase 3** is valuable for evolutionary algorithms because **LSM structure directly impacts read latency** via read amplification. Bad configurations will show degraded read performance, guiding the optimizer toward better configs.

Ready to proceed with Phase 1 implementation?
