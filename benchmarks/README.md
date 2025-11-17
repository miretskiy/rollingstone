# RocksDB Benchmarking for Simulator Modeling

This directory contains empirical performance data and analysis for building accurate CPU and read path models in the LSM simulator.

## Files

### Benchmark Scripts
- **`run_benchmarks.sh`** - Full benchmark suite (10M keys, ~30-60 min)
- **`run_benchmarks_small.sh`** - Quick benchmarks (1M keys, ~5-10 min)
- **`run_timeseries.sh`** - Timeseries workload (Datadog-relevant, 10M keys)

### Results Directories
- **`results/`** - Full benchmark results (10M keys)
- **`results_small/`** - Quick benchmark results (1M keys)
- **`results_timeseries/`** - Timeseries workload results

### Analysis Documents
- **`cpu_cost_analysis.md`** - ⭐ **PRIMARY ANALYSIS** - CPU cost model synthesized from published AWS benchmarks
- **`benchmark_analysis.md`** - Initial analysis from small benchmarks (superseded by cpu_cost_analysis.md)

## Key Findings

### 1. I/O Bound, Not CPU Bound (from AWS Published Data)

**Write Path:**
- Published AWS results: 402-409 MB/s (minimal difference with/without Direct I/O)
- Conclusion: **I/O is the bottleneck**, compression CPU is negligible
- Conservative estimate: Compression uses ≤10% of total time

**Read Path:**
- Direct I/O improves random reads by 38% (189K vs 137K ops/sec)
- Interpretation: CPU cache management overhead is ~28% in standard mode
- But I/O still dominates overall latency

### 2. Compression CPU Estimates

Based on inference from published benchmarks:

| Algorithm | Compression Throughput | Decompression Throughput | Notes |
|-----------|----------------------|-------------------------|-------|
| **Snappy** | ~2000 MB/s | ~3000 MB/s | RocksDB default, very fast |
| **Zstd-3** | ~800 MB/s | ~2000 MB/s | Better compression, still fast |
| **Zstd-9** | ~200 MB/s | ~2000 MB/s | Best compression, slower |
| **None** | Infinite | Infinite | No CPU cost |

*Conservative estimates for modern CPUs (2020+)*

### 3. Timeseries Benchmark (Missing from Published Data)

- **Status**: Script ready (`run_timeseries.sh`)
- **Relevance**: Datadog use case - 1 writer + multiple readers
- **Next**: Run to get realistic time-series workload data

## How to Use

### Quick Test (5-10 minutes):
```bash
cd ~/src/rollingstone/benchmarks
./run_benchmarks_small.sh
```

### Full Benchmarks (30-60 minutes):
```bash
cd ~/src/rollingstone/benchmarks
./run_benchmarks.sh
```

### Timeseries (Datadog-relevant):
```bash
cd ~/src/rollingstone/benchmarks
./run_timeseries.sh
```

## Recommendations for Simulator

### Current State (Write Path Only):

**Don't add full CPU modeling yet** - it's not the bottleneck:
- Use simplified formula: `duration = ioTime` (ignore CPU)
- Focus on I/O throughput tuning
- Compression choice affects **data size** more than **duration**

### When Adding Read Path:

Add workload profiles with CPU parameters:

```go
type WorkloadProfile struct {
    Name                     string
    CompressionCPUMBps       float64  // ~2000 for Snappy
    DecompressionCPUMBps     float64  // ~3000 for Snappy
    TypicalCacheHitRate      float64  // ~0.80 (80%)
    BloomFalsePositiveRate   float64  // ~0.01 (1%)
}
```

Built-in profiles:
- "AWS m5d.2xlarge (NVMe, Snappy)"
- "Mac M1 (NVMe, Snappy)"
- "Mac M1 (NVMe, Zstd)"

### Formula:

**Write path (compaction)**:
```go
compressTime = outputSize / compressionCPUMBps
ioTime = (inputSize + outputSize) / ioThroughputMBps
duration = max(compressTime, ioTime)  // Usually I/O dominates
```

**Read path (cache miss)**:
```go
ioLatency = diskLatencyMs + (blockSize / ioThroughputMBps)
decompressLatency = blockSize / decompressionCPUMBps
totalLatency = ioLatency + decompressLatency  // I/O dominates
```

## References

- **Published Benchmarks**: https://github.com/facebook/rocksdb/wiki/performance-benchmarks
- **Benchmark Tools**: https://github.com/facebook/rocksdb/wiki/Benchmarking-tools
- **Hardware**: AWS m5d.2xlarge (8 CPU, 32GB RAM, NVMe SSD, 117K IOPS)
- **RocksDB Version**: 10.9.0 (our builds)
