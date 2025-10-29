# RollingStone

A **discrete event simulator** for RocksDB LSM trees, built with Go backend and React frontend.

## Quick Start

```bash
./start.sh
```

This single command:
1. Cleans up port 8080
2. Builds frontend (npm) + backend (Go)
3. Starts server on http://localhost:8080

Press **Ctrl+C** to stop, or visit http://localhost:8080/quitquitquit

**Build without running:**
```bash
./build.sh
```

## What It Does

RollingStone simulates RocksDB's LSM tree behavior to help you:
- **Analyze performance** of different RocksDB configurations
- **Visualize** LSM tree structure, compactions, and I/O patterns in real-time
- **Optimize** for specific workloads without running full benchmarks
- **Understand** write amplification, read amplification, and space amplification

## Key Features

### True Discrete Event Simulation
- Event queue with virtual clock (not naive time-stepping)
- Deterministic execution (same config → same results)
- Fast simulation (hours of virtual time in seconds)
- Models I/O contention, write stalls, and compaction scheduling

### Faithful RocksDB Modeling
- **Tiered+Leveled compaction**: L0 overlapping, L1+ sorted runs
- **Compaction scoring**: Priority-based level selection
- **Parallel compactions**: Configurable background jobs
- **File-level operations**: Statistical overlap simulation
- **I/O bandwidth**: Disk contention and throughput limits
- **Write stalls**: When memtables back up

### Interactive UI
- Real-time LSM tree visualization (memtable, L0-L6)
- Adjustable simulation parameters (write rate, I/O profiles)
- Live metrics (amplifications, throughput, latencies)
- Collapsible configuration sections with presets

## Architecture

```
rollingstone/
├── simulator/          # Core DES engine (Go)
│   ├── simulator.go    # Event loop, virtual clock
│   ├── lsm.go          # LSM tree state
│   ├── compactor.go    # Compaction logic
│   ├── metrics.go      # Performance tracking
│   └── events.go       # Event types
├── cmd/server/         # WebSocket server + embedded UI
└── web/                # React frontend (Vite + TypeScript)
```

**Communication**: WebSocket with JSON messages (start, pause, reset, config updates, metrics streaming)

## Configuration

### LSM Tree Parameters
- **Levels**: 3-7 levels (default: 7)
- **Memtable size**: 64-256 MB (default: 64 MB)
- **L0 trigger**: 4 files (default)
- **Level multiplier**: 10x (default)
- **Target file size**: 64 MB (default), grows with level depth

### Workload
- **Write rate**: 0-500 MB/s (adjustable during simulation)
- **I/O profiles**: EBS gp3 (500 MB/s), NVMe (3000 MB/s), HDD (150 MB/s)
- **Compaction parallelism**: 1-8 jobs (default: 2)

## Development

### Requirements
- Go 1.21+
- Node.js 18+

### Running
```bash
./start.sh          # Build + run (one command)
./build.sh          # Build only (frontend + backend)
```

**Note**: We serve the React app as static files from the Go server. No separate npm dev server needed.

### Documentation
- **[Architecture & Protocol](docs/ARCHITECTURE.md)**: System design, WebSocket protocol, deployment
- **[Development Guide](docs/DEVELOPMENT.md)**: Implementation details, UI design, simulation fidelity

## References

Based on RocksDB documentation:
- [Leveled Compaction](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction)
- [RocksDB Tuning Guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)

## License

MIT
