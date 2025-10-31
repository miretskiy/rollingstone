// Message types for WebSocket communication
export interface SimulationConfig {
    writeRateMBps: number;
    memtableFlushSizeMB: number;
    maxWriteBufferNumber: number;
    memtableFlushTimeoutSec: number;
    l0CompactionTrigger: number;
    maxBytesForLevelBaseMB: number;
    levelMultiplier: number;
    targetFileSizeMB: number;
    targetFileSizeMultiplier: number;
    compactionReductionFactor: number;
    maxBackgroundJobs: number;
    maxSubcompactions: number;
    maxCompactionBytesMB: number;
    ioLatencyMs: number;
    ioThroughputMBps: number;
    numLevels: number;
    initialLSMSizeMB: number;
    simulationSpeedMultiplier: number;
    randomSeed: number;
    maxStalledWriteMemoryMB?: number;
}

export interface CompactionStats {
    count: number;
    totalInputFiles: number;
    totalOutputFiles: number;
    totalInputMB: number;
    totalOutputMB: number;
}

export interface SimulationMetrics {
    timestamp: number;
    writeAmplification: number;
    readAmplification: number;
    writeLatencyMs: number;
    readLatencyMs: number;
    totalDataWrittenMB: number;
    totalDataReadMB: number;
    spaceAmplification: number;
    flushThroughputMBps: number;
    compactionThroughputMBps: number;
    totalWriteThroughputMBps: number;
    perLevelThroughputMBps: Record<number, number>;
    maxSustainableWriteRateMBps?: number; // Maximum sustainable write rate (conservative estimate)
    minSustainableWriteRateMBps?: number; // Minimum sustainable write rate (worst-case estimate)
    compactionsSinceUpdate?: Record<number, CompactionStats>; // Per-level aggregate compaction activity
    inProgressCount?: number;
    inProgressDetails?: Array<{
        inputMB: number;
        outputMB: number;
        fromLevel: number;
        toLevel: number;
    }>;
    stalledWriteCount?: number;
    maxStalledWriteCount?: number;
    stallDurationSeconds?: number;
    isStalled?: boolean;
    isOOMKilled?: boolean;
}

export interface SSTFile {
    id: string;
    sizeMB: number;
    ageSeconds: number;
}

export interface LevelState {
    level: number;
    totalSizeMB: number;
    targetSizeMB?: number;
    fileCount: number;
    files: SSTFile[];
}

export interface ActiveCompactionInfo {
    fromLevel: number;
    toLevel: number;
    sourceFileCount: number;
    targetFileCount: number;
    isIntraL0: boolean;
}

export interface SimulationState {
    virtualTime: number;
    memtableCurrentSizeMB: number;
    levels: LevelState[];
    totalSizeMB: number;
    activeCompactions?: number[]; // Levels currently being compacted
    activeCompactionInfos?: ActiveCompactionInfo[]; // Detailed compaction info
    numImmutableMemtables?: number; // Number of immutable memtables waiting to flush
    immutableMemtableSizesMB?: number[]; // Sizes of immutable memtables waiting to flush
}

export interface SimulationEvent {
    timestamp: number;
    type: 'flush' | 'compaction' | 'read' | 'write';
    message: string;
    level?: number;
}

// WebSocket message types
export type WSMessage =
    | { type: 'start' }
    | { type: 'pause' }
    | { type: 'reset' }
    | { type: 'step' }
    | { type: 'config_update'; config: Partial<SimulationConfig> }
    | { type: 'status'; running: boolean; config: SimulationConfig }
    | { type: 'metrics'; metrics: SimulationMetrics }
    | { type: 'state'; state: SimulationState }
    | { type: 'event'; event: SimulationEvent }
    | { type: 'log'; log: string };

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

