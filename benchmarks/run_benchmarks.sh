#!/bin/bash
# RocksDB db_bench benchmark runner
# Collects empirical CPU and read performance data for simulator modeling

set -e

# Need to run from rocksdb directory for dylib loading
cd ~/src/rocksdb

DB_BENCH=./db_bench
DB_DIR=/tmp/rocksdb_bench_test
RESULTS_DIR=~/src/rollingstone/benchmarks/results

# Create results directory
mkdir -p "$RESULTS_DIR"

# Clean up any previous test database
rm -rf "$DB_DIR"

echo "=== RocksDB Benchmark Suite ==="
echo "DB Directory: $DB_DIR"
echo "Results Directory: $RESULTS_DIR"
echo ""

# Common parameters
NUM_KEYS=10000000  # 10M keys
VALUE_SIZE=1000    # 1KB values
CACHE_SIZE=1073741824  # 1GB block cache
WRITE_BUFFER_SIZE=67108864  # 64MB memtable
MAX_BACKGROUND_JOBS=2

# Helper function to run benchmark
run_benchmark() {
    local bench_name=$1
    local compression=$2
    local output_file="$RESULTS_DIR/${bench_name}_${compression}.txt"

    echo "Running: $bench_name with compression=$compression"
    echo "Output: $output_file"

    $DB_BENCH \
        --benchmarks="$bench_name" \
        --compression_type="$compression" \
        --num="$NUM_KEYS" \
        --value_size="$VALUE_SIZE" \
        --cache_size="$CACHE_SIZE" \
        --write_buffer_size="$WRITE_BUFFER_SIZE" \
        --max_background_jobs="$MAX_BACKGROUND_JOBS" \
        --statistics=true \
        --stats_interval=10000000 \
        --report_interval_seconds=5 \
        --db="$DB_DIR" \
        2>&1 | tee "$output_file"

    echo "Completed: $bench_name with compression=$compression"
    echo ""
}

# Phase 1: Write benchmarks (fillseq)
echo "=== Phase 1: Write Performance (fillseq) ==="
rm -rf "$DB_DIR"
run_benchmark "fillseq" "none"

rm -rf "$DB_DIR"
run_benchmark "fillseq" "snappy"

rm -rf "$DB_DIR"
run_benchmark "fillseq" "zstd"

# Phase 2: Read benchmarks (readrandom) - reuse last database
echo "=== Phase 2: Read Performance (readrandom) ==="
run_benchmark "readrandom" "snappy"

# Phase 3: Mixed workload (readwhilewriting)
echo "=== Phase 3: Mixed Workload (readwhilewriting) ==="
run_benchmark "readwhilewriting" "snappy"

# Phase 4: Scan performance (seekrandom)
echo "=== Phase 4: Scan Performance (seekrandom) ==="
run_benchmark "seekrandom" "snappy"

echo "=== Benchmark Suite Complete ==="
echo "Results saved to: $RESULTS_DIR"
