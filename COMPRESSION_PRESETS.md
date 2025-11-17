# Compression Algorithm Presets

This document describes the compression presets available in the RocksDB simulator and their performance characteristics.

## Overview

The simulator provides four compression presets based on real-world benchmark data. These presets configure compression ratio, CPU throughput, and block size to accurately model different compression algorithms used in production RocksDB deployments.

**Why Compression Matters:**
- Affects physical storage size (compression ratio)
- Adds CPU overhead to compaction and flush operations
- Impacts operation latency (modeled additively: decompress → merge → compress → write)
- Production data shows ~12.5% CPU time spent on compression (Mork service profiling)

## Available Presets

### 1. LZ4 (Default)
**Best for:** Balanced performance - fast compression/decompression with decent compression ratio

```go
config.WithLZ4Compression()
```

**Parameters:**
- Compression Factor: `0.85` (~15% size reduction)
- Compression Speed: `750 MB/s` (single-threaded)
- Decompression Speed: `3700 MB/s` (single-threaded)
- Block Size: `4 KB` (RocksDB default)

**Use Cases:**
- General purpose workloads
- Read-heavy workloads (fast decompression)
- Production default for most RocksDB deployments

**Characteristics:**
- Very fast decompression (5x faster than compression)
- Lower CPU overhead compared to other algorithms
- Moderate compression ratio

---

### 2. Snappy
**Best for:** Legacy compatibility, moderate CPU usage acceptable

```go
config.WithSnappyCompression()
```

**Parameters:**
- Compression Factor: `0.83` (~17% size reduction)
- Compression Speed: `530 MB/s` (single-threaded)
- Decompression Speed: `1800 MB/s` (single-threaded)
- Block Size: `4 KB`

**Use Cases:**
- Legacy RocksDB deployments (original default)
- When compatibility with older systems is required
- Balanced workloads with moderate I/O

**Characteristics:**
- Slightly better compression than LZ4
- Slower decompression than LZ4
- More CPU overhead than LZ4

---

### 3. Zstd (Zstandard)
**Best for:** Storage-optimized workloads, CPU is abundant, I/O is expensive

```go
config.WithZstdCompression()
```

**Parameters:**
- Compression Factor: `0.70` (~30% size reduction, level 3 default)
- Compression Speed: `470 MB/s` (single-threaded, level 3)
- Decompression Speed: `1380 MB/s` (single-threaded)
- Block Size: `4 KB`

**Use Cases:**
- Storage-constrained environments
- Cold data archival
- When disk I/O is more expensive than CPU cycles
- Write-heavy workloads where storage savings outweigh CPU cost

**Characteristics:**
- Best compression ratio
- Highest CPU overhead
- Slower compression and decompression
- Tunable compression levels (level 3 is RocksDB default)

---

### 4. None (Uncompressed)
**Best for:** Pre-compressed data, maximum CPU conservation

```go
config.WithNoCompression()
```

**Parameters:**
- Compression Factor: `1.0` (no compression)
- Compression Speed: `0` (infinite - no CPU cost)
- Decompression Speed: `0` (infinite - no CPU cost)
- Block Size: `4 KB`

**Use Cases:**
- Data already compressed (images, video, encrypted data)
- Incompressible data
- CPU-constrained environments
- Testing/debugging compression impact

**Characteristics:**
- No CPU overhead
- Largest storage footprint
- Maximum I/O bandwidth (no compression/decompression delay)

---

## Benchmark Data Sources

All throughput values are based on real-world benchmarks:

- **LZ4:** https://github.com/lz4/lz4
- **Snappy:** https://github.com/google/snappy
- **Zstd:** https://facebook.github.io/zstd/

**Important Notes:**
- Benchmarks are single-threaded performance
- Actual throughput varies by data compressibility
- Small blocks (4 KB) achieve worse compression ratios than larger blocks (16-64 KB)
- Production measurements (Mork): 4m40s total CPU, 35s on LZ4 = 12.5% CPU overhead

---

## Additive Duration Model

The simulator models compression/decompression time **additively** with I/O operations:

**Compaction:**
```
duration = readIO + decompress + compress + writeIO + seek
```

**Flush:**
```
duration = compress + writeIO + seek
```

**Example** (64 MB compaction with LZ4):
- Read I/O: 64 MB / 500 MB/s = 128 ms
- Decompress: 64 MB / 3700 MB/s = 17 ms
- Compress: 54 MB / 750 MB/s = 72 ms (after 0.85 compression)
- Write I/O: 54 MB / 500 MB/s = 108 ms
- Seek: 1 ms
- **Total: 326 ms** (vs 257 ms without compression modeling)

---

## Customization

All preset parameters can be overridden to model workload-specific characteristics:

**In Go:**
```go
config := simulator.DefaultConfig()
config.CompressionFactor = 0.80           // Custom compression ratio
config.CompressionThroughputMBps = 600    // Custom compression speed
config.DecompressionThroughputMBps = 3000 // Custom decompression speed
config.BlockSizeKB = 16                   // Larger blocks compress better
```

**In Web UI:**
- Navigate to **Workload & Traffic Pattern → Compression**
- Select a preset (LZ4, Snappy, Zstd, None)
- Adjust individual parameters as needed

---

## Choosing the Right Preset

| Priority | Recommended Preset | Rationale |
|----------|-------------------|-----------|
| **Balanced Performance** | LZ4 | Fast decompression, low CPU overhead, production default |
| **Storage Savings** | Zstd | 30% compression, worth CPU cost if storage is expensive |
| **Legacy Compatibility** | Snappy | RocksDB's original default, widely supported |
| **Maximum Speed** | None | No CPU overhead, use for pre-compressed or incompressible data |
| **Read Performance** | LZ4 | 3700 MB/s decompression (fastest) |
| **Write Performance** | None or LZ4 | No compression or minimal CPU overhead |

---

## Related Documentation

- **Phase 1 Implementation:** See commit message for detailed rationale and production analysis
- **Production Analysis:** `PRODUCTION_ANALYSIS_FINAL.md` and `DATADOG_ANALYSIS_CLARIFICATIONS.md`
- **Implementation Plan:** `IMPLEMENTATION_PLAN.md` for design decisions

---

## Future Enhancements

Potential improvements not yet implemented:

1. **Multi-threaded compression modeling** - RocksDB can parallelize compression within subcompactions
2. **Per-level compression settings** - Different compression for hot vs cold data
3. **Compression level tuning** - Zstd supports levels 1-22 with different speed/ratio tradeoffs
4. **Adaptive compression** - Switch algorithms based on data compressibility
5. **Block cache modeling** - Impact of compressed vs uncompressed block cache

For now, the simulator focuses on single-threaded performance as this matches the additive model and provides good fidelity for most use cases.
