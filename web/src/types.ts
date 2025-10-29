// Message types for WebSocket communication
export interface SimulationConfig {
    writeRateMBps: number;
    memtableFlushSizeMB: number;
    maxWriteBufferNumber: number;
    l0CompactionTrigger: number;
    maxBytesForLevelBaseMB: number;
    levelMultiplier: number;
    targetFileSizeMB: number;
    compactionReductionFactor: number;
    maxBackgroundJobs: number;
    maxSubcompactions: number;
    maxCompactionBytesMB: number;
    ioLatencyMs: number;
    ioThroughputMBps: number;
    numLevels: number;
    initialLSMSizeMB: number;
    simulationSpeedMultiplier: number;
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
    compactionsSinceUpdate: Record<number, CompactionStats>;
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
    targetSizeMB: number;
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
    numImmutableMemtables?: number; // Count of frozen memtables waiting to flush
    immutableMemtableSizesMB?: number[]; // Sizes of frozen memtables waiting to flush
    levels: LevelState[];
    totalSizeMB: number;
    activeCompactions?: number[]; // Levels currently being compacted (simple list)
    activeCompactionInfos?: ActiveCompactionInfo[]; // Detailed compaction info
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
    | { type: 'error'; error: string };

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

