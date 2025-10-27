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
    compactionReductionFactor: number;
    maxBackgroundJobs: number;
    maxSubcompactions: number;
    ioLatencyMs: number;
    ioThroughputMBps: number;
    numLevels: number;
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
    inProgressCount?: number;
    inProgressDetails?: Array<{
        inputMB: number;
        outputMB: number;
        fromLevel: number;
        toLevel: number;
    }>;
}

export interface SSTFile {
    id: string;
    sizeMB: number;
    ageSeconds: number;
}

export interface LevelState {
    level: number;
    totalSizeMB: number;
    fileCount: number;
    files: SSTFile[];
}

export interface SimulationState {
    virtualTime: number;
    memtableCurrentSizeMB: number;
    levels: LevelState[];
    totalSizeMB: number;
    activeCompactions?: number[]; // Levels currently being compacted
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
    | { type: 'event'; event: SimulationEvent };

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

