# RocksDB Benchmark Analysis
## Empirical CPU and Read Performance Data for Simulator Modeling

**Date**: November 15, 2025
**Hardware**: Mac ARM64 (Apple Silicon)
**RocksDB Version**: 10.9.0
**Dataset**: 1M keys × 1KB values = ~1GB total data

---

## Executive Summary

Benchmarks reveal that on modern ARM processors (Apple Silicon):
1. **Compression improves write throughput** (3-5%) - writing less data outweighs CPU cost
2. **Point reads are extremely fast** (7.4M ops/sec) when cached
3. **CPU is NOT the bottleneck** for writes on this hardware
4. **Decompression overhead is minimal** for cached reads

---

## Detailed Results

### 1. Write Performance (fillseq)

Sequential writes with different compression algorithms:

| Compression | Throughput | Micros/op | Notes |
|-------------|------------|-----------|-------|
| **None**    | 355.7 MB/s | 2.724 µs  | Baseline (no CPU overhead) |
| **Snappy**  | 367.0 MB/s | 2.640 µs  | **3.2% faster** than no compression |
| **Zstd**    | 372.5 MB/s | 2.601 µs  | **4.7% faster** than no compression |

**Key Insight**: Compression actually **speeds up** writes on Apple Silicon:
- Compression CPU cost < I/O time saved from writing less data
- Snappy compression is "free" or beneficial
- Even Zstd (better compression, more CPU) is faster overall

**Implications for Simulator**:
- For Mac ARM64 profile: Compression CPU overhead = 0 (or negative!)
- Compaction duration driven by I/O, not CPU
- Formula: `duration = IO_time_only` (no CPU bottleneck)

---

### 2. Read Performance (readrandom)

Random point lookups with Snappy compression:

| Metric | Value | Notes |
|--------|-------|-------|
| **Ops/sec** | 7,360,193 | 7.36M ops/sec |
| **Latency** | 0.136 µs | Sub-microsecond! |
| **Cache hits** | 100% | All reads from cache (0 of 1M found on disk) |

**Key Insight**: Reads are memory-bound, not CPU-bound
- Block cache lookups dominate (< 1 µs)
- Decompression rarely happens (cache hit rate high)
- When decompression occurs, it's fast (<< 1 µs on Apple Silicon)

**Implications for Simulator**:
- Read latency = cache_lookup_time (if hit) OR disk_io_time + decompress_time (if miss)
- Decompress_time negligible for Snappy on ARM (< 1 µs)
- Cache hit rate is the dominant factor

---

### 3. Mixed Workload (readwhilewriting)

Concurrent reads and writes with Snappy:

| Metric | Value | Notes |
|--------|-------|-------|
| **Read ops/sec** | 324,625 | While writes ongoing |
| **Throughput** | 65 MB/s | Combined read+write |
| **Cache hit rate** | 20.7% | 206,545 of 1M found |
| **Latency** | 3.080 µs | Mixed operation latency |

**Key Insight**: Workload contention, not CPU
- Read throughput drops when writes active (I/O contention)
- Lower cache hit rate = more disk reads
- CPU still not bottleneck

---

### 4. Scan Performance (seekrandom)

Random range scans with Snappy:

| Metric | Value | Notes |
|--------|-------|-------|
| **Ops/sec** | 3,900,308 | 3.9M seeks/sec |
| **Latency** | 0.256 µs | Per seek operation |
| **Cache hits** | 100% | All seeks from cache |

**Key Insight**: Iterator positioning is extremely fast
- Scan initiation overhead minimal
- Actual scan throughput depends on range size
- Decompression amortized across scan

---

## CPU vs I/O Bottleneck Analysis

### Write Path (fillseq results):

**Without compression (baseline I/O only)**:
- Throughput: 355.7 MB/s
- This represents pure I/O limit

**With Snappy compression**:
- Throughput: 367.0 MB/s (FASTER!)
- Snappy compress rate: ~400-600 MB/s (from RocksDB docs)
- Observed: No CPU bottleneck - compression is "free"

**With Zstd compression**:
- Throughput: 372.5 MB/s (even FASTER!)
- Zstd is slower than Snappy but still no bottleneck
- Benefit of writing less data > CPU cost

**Conclusion**: On Mac ARM64, **I/O is the bottleneck**, not CPU.

---

## Compression CPU Throughput Estimates

Based on RocksDB documentation and our observations:

### Snappy (RocksDB default):
- **Compression**: ~500 MB/s (Intel Core 2 baseline from docs)
- **Decompression**: ~700 MB/s (Intel Core 2 baseline from docs)
- **Apple Silicon**: Likely 2-3x faster = ~1000-1500 MB/s compress, ~1400-2100 MB/s decompress
- **Observation**: No measurable overhead in our benchmarks

### Zstd (better compression):
- **Level 3** (default): ~100-200 MB/s compress, ~600-800 MB/s decompress
- **Apple Silicon**: Likely 2x faster = ~200-400 MB/s compress, ~1200-1600 MB/s decompress
- **Observation**: Still no bottleneck in our tests

### No Compression:
- CPU throughput: Infinite (no CPU cost)
- Baseline: 355.7 MB/s write throughput

---

## Implications for Simulator

### Current State:
```go
// Current compaction duration (I/O only):
duration = (inputSize + outputSize) / ioThroughputMBps
```

### Proposed Enhancement:

```go
// Add CPU time calculation:
cpuCompressTime = outputSize / cpuCompressionThroughputMBps
cpuDecompressTime = inputSize / cpuDecompressionThroughputMBps

// Duration is max of I/O and CPU:
ioTime = (inputSize + outputSize) / ioThroughputMBps
cpuTime = cpuCompressTime + cpuDecompressTime
duration = max(ioTime, cpuTime)

// Track which is bottleneck:
if cpuTime > ioTime {
    metrics.CPUBoundCompactionCount++
} else {
    metrics.IOBoundCompactionCount++
}
```

### Workload Profiles:

Create built-in profiles based on hardware + compression:

```go
type WorkloadProfile struct {
    Name                      string
    CompressionCPUMBps        float64  // Compression throughput
    DecompressionCPUMBps      float64  // Decompression throughput
    BlockCacheHitRate         float64  // Typical cache hit rate
    BloomFalsePositiveRate    float64  // Typical false positive rate
    ReadLatencyCacheHitUs     float64  // Latency for cache hit
    ReadLatencyCacheMissUs    float64  // Latency for cache miss + decompress
}

var BuiltInProfiles = map[string]WorkloadProfile{
    "Mac M1 - Snappy": {
        CompressionCPUMBps:      1500.0,  // Very fast, no bottleneck
        DecompressionCPUMBps:    2000.0,
        BlockCacheHitRate:       0.80,    // 80% typical
        BloomFalsePositiveRate:  0.01,    // 1% FP rate
        ReadLatencyCacheHitUs:   0.5,     // Sub-microsecond
        ReadLatencyCacheMissUs:  10.0,    // Disk I/O dominates
    },
    "Mac M1 - Zstd": {
        CompressionCPUMBps:      400.0,   // Slower but still fast
        DecompressionCPUMBps:    1500.0,
        BlockCacheHitRate:       0.80,
        BloomFalsePositiveRate:  0.01,
        ReadLatencyCacheHitUs:   0.5,
        ReadLatencyCacheMissUs:  11.0,    // Slightly slower decompress
    },
    "Mac M1 - No Compression": {
        CompressionCPUMBps:      999999.0, // Infinite (no CPU cost)
        DecompressionCPUMBps:    999999.0,
        BlockCacheHitRate:       0.80,
        BloomFalsePositiveRate:  0.01,
        ReadLatencyCacheHitUs:   0.5,
        ReadLatencyCacheMissUs:  8.0,      // Faster (no decompress)
    },
    // Future: Add AWS m5.xlarge, etc.
}
```

---

## Next Steps

1. **Phase 1: Add CPU modeling to write path (compactions)**
   - Add `cpuBusyUntil` tracker (like `diskBusyUntil`)
   - Calculate `cpuTime` for compress/decompress
   - Use `max(ioTime, cpuTime)` for duration
   - Add CPU utilization metrics

2. **Phase 2: Implement read path**
   - Model bloom filter probes
   - Model block cache lookups
   - Model decompression on cache miss
   - Track cache hit rate

3. **Phase 3: Add workload profile selector in UI**
   - Dropdown: "Workload Profile: [Mac M1 - Snappy ▼]"
   - Auto-populate CPU throughput values
   - Show whether CPU or I/O bound in metrics

4. **Phase 4: Run full benchmarks on AWS/Linux**
   - Get production-grade CPU numbers
   - Compare with Mac ARM64
   - Add AWS EC2 profiles

---

## Conclusion

**For Mac ARM64 (Apple Silicon):**
- CPU is **NOT the bottleneck** for writes (I/O dominates)
- Compression actually **improves** throughput
- Decompression overhead is **minimal** for reads
- Cache hit rate is the **dominant factor** for read performance

**For simulator:**
- Start with workload profiles (Mac M1 as baseline)
- Model CPU + I/O independently: `duration = max(ioTime, cpuTime)`
- Add read path modeling (bloom filters, cache, decompression)
- Show users whether their config is CPU-bound or I/O-bound

**Full benchmarks** (10M keys) running in background - will provide more precise numbers for production workloads.
