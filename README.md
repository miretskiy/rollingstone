# RollingStone

A **discrete event simulator** for RocksDB LSM trees, built with Go backend and React frontend.

## Quick Start

**One command to rule them all:**

```bash
./start.sh
# Automatically builds frontend (if needed) and starts backend
# Open browser to http://localhost:8080
```

**Manual mode:**

```bash
# Build frontend once
cd web && npm install && npm run build && cd ..

# Build and run backend
go build -o /tmp/rollingstone cmd/server/main.go
/tmp/rollingstone

# Open browser to http://localhost:8080
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
- Node.js 18+ (for frontend development)

### Build
```bash
# Backend
go build -o /tmp/rollingstone cmd/server/main.go

# Frontend (development)
cd web && npm install && npm run dev
```

### Documentation
- **[Architecture & Protocol](docs/ARCHITECTURE.md)**: System design, WebSocket protocol, deployment
- **[Development Guide](docs/DEVELOPMENT.md)**: Implementation details, UI design, simulation fidelity

## References

Based on RocksDB documentation:
- [Leveled Compaction](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction)
- [RocksDB Tuning Guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)

## License

MIT
