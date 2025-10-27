# RollingStone Architecture

## System Overview

RollingStone is a discrete event simulator (DES) for RocksDB LSM trees with a client-server architecture:

```
┌──────────────────────────────────────────────────────────────┐
│ Browser (http://localhost:8080)                               │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ React Frontend (Vite + TypeScript)                      │ │
│  │  - Zustand store                                        │ │
│  │  - Real-time visualization                              │ │
│  │  - Configuration UI                                     │ │
│  └────────────────────────┬────────────────────────────────┘ │
└─────────────────────────────┼───────────────────────────────┘
                              │ WebSocket (JSON messages)
┌─────────────────────────────┼────────────────────────────────┐
│ Go Backend (:8080)          │                                │
│  ┌────────────────────────┬┴──────────────────────────────┐ │
│  │ WebSocket Server       │ Embedded Static Files         │ │
│  │  - gorilla/websocket   │  - HTML/JS/CSS (go:embed)     │ │
│  │  - Command dispatcher  │                               │ │
│  └────────────┬───────────┴───────────────────────────────┘ │
│               │                                              │
│  ┌────────────┴─────────────────────────────────────────┐   │
│  │ Discrete Event Simulator (simulator/)                │   │
│  │  - Event queue (priority queue)                      │   │
│  │  - Virtual clock                                     │   │
│  │  - LSM tree state (MemTable, L0-L6)                  │   │
│  │  - Compaction engine (scoring, file selection)       │   │
│  │  - I/O modeling (disk contention, write stalls)      │   │
│  │  - Metrics tracking (amplifications, throughput)     │   │
│  └──────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. Discrete Event Simulator (`simulator/`)

The heart of RollingStone. A pull-based simulator where the UI drives execution:

**Key files:**
- `simulator.go`: Event loop, virtual clock, state machine
- `lsm.go`: LSM tree data structures (memtable, levels, files)
- `compactor.go`: Compaction logic (scoring, file selection, execution)
- `metrics.go`: Performance tracking (amplifications, throughput)
- `events.go`: Event types (Write, Flush, Compaction, CompactionCheck)
- `event_queue.go`: Priority queue for discrete events

**Event Types:**
- **WriteEvent**: Incoming writes (driven by configured rate)
- **FlushEvent**: Memtable → L0 flush (when size/time threshold reached)
- **CompactionEvent**: L𝑛 → L𝑛₊₁ compaction (when files are ready)
- **CompactionCheckEvent**: Background thread checking for compaction needs

**Virtual Time:**
- All operations use simulation time (seconds since start)
- No real-time delays or sleepers
- UI controls pacing via `Step()` calls

### 2. WebSocket Server (`cmd/server/main.go`)

Handles bidirectional communication:

**Responsibilities:**
- Serve embedded static files (HTML/JS/CSS)
- WebSocket endpoint at `/ws`
- Command dispatch (start, pause, reset, config_update)
- UI update loop (500ms ticker)
- Graceful shutdown (`/quitquitquit`)

**Concurrency Model:**
- Simulation runs in main goroutine (pull-based)
- UI update loop in separate goroutine
- `safeConn` mutex wrapper prevents concurrent WebSocket writes

### 3. React Frontend (`web/`)

Modern, responsive UI built with:
- **React 18** + **TypeScript**
- **Vite** (build tool)
- **Tailwind CSS v4** (styling)
- **Zustand** (state management)
- **Radix UI** (headless components)
- **Recharts** (charts - currently disabled for performance)
- **Lucide React** (icons)

**Key Components:**
- `SimulationControls.tsx`: Play/pause, reset, config sliders
- `LSMTreeVisualization.tsx`: Tree structure, file counts, sizes
- `MetricsDashboard.tsx`: Amplifications, throughput, active compactions
- `store.ts`: Zustand store (connection, state, metrics, config)

## WebSocket Protocol

### Message Types

#### Client → Server

```typescript
// Start simulation
{ type: "start" }

// Pause simulation
{ type: "pause" }

// Reset simulation
{ type: "reset" }

// Update configuration (static params require reset; dynamic are live)
{
  type: "config_update",
  config: {
    writeRateMBps?: number,          // Dynamic
    memtableFlushSizeMB?: number,    // Static
    maxBackgroundJobs?: number,      // Static
    // ... see types.ts for full list
  }
}
```

#### Server → Client

```typescript
// Status update (sent on start/pause/reset)
{
  type: "status",
  running: boolean,
  config: SimulationConfig  // Full config object
}

// Metrics update (sent every 500ms when running)
{
  type: "metrics",
  metrics: {
    timestamp: number,
    writeAmplification: number,
    readAmplification: number,
    spaceAmplification: number,
    totalWriteThroughputMBps: number,
    flushThroughputMBps: number,
    compactionThroughputMBps: number,
    perLevelThroughputMBps: Record<number, number>,
    inProgressCount: number,
    inProgressDetails: Array<{
      inputMB: number,
      outputMB: number,
      fromLevel: number,
      toLevel: number
    }>
  }
}

// State update (LSM tree snapshot)
{
  type: "state",
  state: {
    virtualTime: number,
    memtableCurrentSizeMB: number,
    totalSizeMB: number,
    levels: Array<{
      level: number,
      fileCount: number,
      totalSizeMB: number,
      files: Array<{
        id: string,
        sizeMB: number,
        ageSeconds: number
      }>
    }>,
    activeCompactions: number[]  // Levels currently compacting
  }
}
```

### Configuration: Static vs Dynamic

**Static parameters** (require simulation reset):
- LSM structure: `numLevels`, `memtableFlushSizeMB`, `l0CompactionTrigger`
- Compaction: `maxBytesForLevelBaseMB`, `levelMultiplier`, `targetFileSizeMB`, `maxBackgroundJobs`
- I/O: `ioThroughputMBps`, `ioLatencyMs`

These are **disabled in UI** while simulation is running.

**Dynamic parameters** (adjustable live):
- `writeRateMBps`: Incoming write traffic

## Deployment

### Development
- Run backend: `go run cmd/server/main.go`
- Run frontend: `cd web && npm run dev` (port 3000)
- Access: `http://localhost:3000` (Vite proxy to :8080)

### Production (Single Binary)
- Build frontend: `cd web && npm run build`
- Embed dist files in Go binary (go:embed)
- Run: `./rollingstone`
- Access: `http://localhost:8080`

### Future: Backend Auto-Start Frontend
Plan to have `cmd/server/main.go`:
1. Check if `web/dist/` exists
2. If not, run `npm run build` automatically
3. Start WebSocket server
4. Serve embedded files
5. On shutdown, cleanup

This ensures single command: `./rollingstone` → full stack running.

## Performance Considerations

### Browser Memory
- **Limit**: Send max 20 files per level (prevent DOM bloat)
- **Throttle**: UI updates every 500ms (not 50ms)
- **Disable**: Charts currently disabled (Recharts memory leak)
- **Cleanup**: Metrics history capped at 500 entries

### Simulation Speed
- **Event-driven**: Processes millions of events/sec
- **Pull-based**: UI controls pacing (no runaway simulation)
- **Deterministic**: Same seed → same results (for testing)

### Throughput Calculation
- **Instantaneous**: 100ms window around current time
- **Capped**: Never exceeds physical disk limit (proportional scaling)
- **In-progress tracking**: Shows active I/O operations

## Error Handling

### Write Stalls
When `numImmutableMemtables >= maxWriteBufferNumber`:
- Incoming WriteEvent rescheduled with 100ms delay
- Simulates RocksDB's write stall behavior

### Disk Contention
- `diskBusyUntil`: Token bucket for sequential I/O
- Flush/Compaction start time = `max(virtualTime, diskBusyUntil)`
- Duration = `(inputSize + outputSize) / ioThroughputMBps`

### WebSocket Concurrency
- `safeConn` mutex wrapper prevents concurrent writes
- UI loop and command handler serialized

## References

- [RocksDB Leveled Compaction](https://github.com/facebook/rocksdb/wiki/Leveled-Compaction)
- [Discrete Event Simulation](https://en.wikipedia.org/wiki/Discrete-event_simulation)

