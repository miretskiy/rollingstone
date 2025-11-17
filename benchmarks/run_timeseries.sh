#!/bin/bash
# RocksDB timeseries benchmark - relevant for Datadog use case
# Tests: 1 writer generates time series data, multiple readers doing random reads on id

set -e

cd ~/src/rocksdb

DB_DIR=/tmp/rocksdb_timeseries_test
RESULTS_DIR=~/src/rollingstone/benchmarks/results_timeseries

mkdir -p "$RESULTS_DIR"
rm -rf "$DB_DIR"

echo "=== RocksDB Timeseries Benchmark ==="
echo "Workload: 1 writer + multiple readers (realistic for time-series monitoring)"
echo "DB Directory: $DB_DIR"
echo "Results Directory: $RESULTS_DIR"
echo ""

# Timeseries parameters (10M keys for realistic test)
NUM_KEYS=10000000
VALUE_SIZE=1000    # 1KB values (typical metric payload)
CACHE_SIZE=1073741824  # 1GB block cache
WRITE_BUFFER_SIZE=67108864  # 64MB memtable
MAX_BACKGROUND_JOBS=2

# Run timeseries benchmark with different compression
for compression in "none" "snappy" "zstd"; do
    echo "=== Running timeseries with compression=$compression ==="
    output_file="$RESULTS_DIR/timeseries_${compression}.txt"

    ./db_bench \
        --benchmarks="timeseries" \
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

    echo "Completed: timeseries with compression=$compression"
    echo ""

    # Clean DB for next test
    rm -rf "$DB_DIR"
done

echo "=== Timeseries Benchmark Complete ==="
echo "Results saved to: $RESULTS_DIR"
echo ""
echo "=== Performance Summary ==="
for f in "$RESULTS_DIR"/*.txt; do
    echo "File: $(basename $f)"
    grep "timeseries" "$f" | grep "micros/op" || echo "  (no summary found)"
done
