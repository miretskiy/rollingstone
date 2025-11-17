// Traffic distribution types
export type TrafficModel = "constant" | "advanced";

export interface TrafficDistributionConfig {
    model: TrafficModel;
    writeRateMBps?: number; // For constant model
    baseRateMBps?: number; // For advanced model
    burstMultiplier?: number;
    lognormalSigma?: number;
    onMeanSeconds?: number;
    offMeanSeconds?: number;
    erlangK?: number;
    spikeRatePerSec?: number;
    spikeMeanDur?: number;
    spikeAmplitudeMean?: number;
    spikeAmplitudeSigma?: number;
    capacityLimitMB?: number;
    queueMode?: "drop" | "queue";
}

export interface OverlapDistributionConfig {
    type: "uniform" | "exponential" | "geometric" | "fixed";
    geometricP?: number;
    exponentialLambda?: number;
    fixedPercentage?: number; // For fixed: percentage of level below that overlaps (0.0 to 1.0)
}

// Read path modeling types
export type LatencyDistributionType = "fixed" | "exponential" | "lognormal";

export interface LatencySpec {
    distribution: LatencyDistributionType;
    mean: number; // Mean latency in milliseconds
}

export interface ReadWorkloadConfig {
    enabled: boolean;
    requestsPerSec: number; // Total read requests per second

    // Traffic variability (simpler than write traffic model)
    requestRateVariability?: number; // Coefficient of variation for request rate (0 = constant, 0.2 = 20% std dev)

    // Request type distribution (percentages, should sum to ~1.0)
    cacheHitRate: number;      // Percentage hitting block cache (e.g., 0.90)
    bloomNegativeRate: number; // Percentage that are bloom filter negatives (e.g., 0.02)
    scanRate: number;          // Percentage that are range scans (e.g., 0.05)
    // Remaining % = point lookups with cache miss

    // Latency specifications per request type
    cacheHitLatency: LatencySpec;      // Latency for cache hits
    bloomNegativeLatency: LatencySpec; // Latency for bloom filter negatives
    pointLookupLatency: LatencySpec;   // Base latency for point lookups (scaled by read amp)
    scanLatency: LatencySpec;          // Latency for range scans

    // Request characteristics
    avgScanSizeKB: number; // Average scan size in KB
}

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
    deduplicationFactor: number;
    compressionFactor: number;
    compressionThroughputMBps: number;
    decompressionThroughputMBps: number;
    blockSizeKB: number;
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
    compactionStyle?: "leveled" | "universal"; // Compaction strategy (default "universal")
    maxSizeAmplificationPercent?: number; // max_size_amplification_percent for universal compaction (default 200%)
    levelCompactionDynamicLevelBytes?: boolean; // level_compaction_dynamic_level_bytes for leveled compaction (default false)
    enableWAL?: boolean; // Enable Write-Ahead Log (default true)
    walSync?: boolean; // Sync WAL after each write (default true)
    walSyncLatencyMs?: number; // fsync() latency in milliseconds (default 1.5ms)
    trafficDistribution?: TrafficDistributionConfig;
    overlapDistribution?: OverlapDistributionConfig;
    readWorkload?: ReadWorkloadConfig; // Read path modeling configuration (undefined = disabled)
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
    walBytesWritten: number;
    spaceAmplification: number;
    flushThroughputMBps: number;
    compactionThroughputMBps: number;
    totalWriteThroughputMBps: number;
    perLevelThroughputMBps: Record<number, number>;
    maxSustainableWriteRateMBps?: number; // Maximum sustainable write rate (conservative estimate)
    minSustainableWriteRateMBps?: number; // Minimum sustainable write rate (worst-case estimate)
    lastCompactionDurationSec?: number; // Duration of most recent compaction in seconds
    lastCompactionThroughputMBps?: number; // Throughput of most recent compaction (input MB / duration)
    compactionsSinceUpdate?: Record<number, CompactionStats>; // Per-level aggregate compaction activity
    totalCompactionsCompleted?: number; // Monotonic counter of total compactions completed (for rate calculation)
    diskUtilizationPercent?: number; // Percentage of disk bandwidth used (0-100%)
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
    avgReadLatencyMs?: number;  // Average read latency across all request types
    p50ReadLatencyMs?: number;  // P50 (median) read latency
    p99ReadLatencyMs?: number;  // P99 read latency
    readBandwidthMBps?: number; // Disk bandwidth consumed by reads
    currentReadReqsPerSec?: number; // Current actual read requests/sec (with variability applied)
    // Read request type breakdown (requests per second)
    cacheHitsPerSec?: number;      // Cache hits per second
    bloomNegativesPerSec?: number; // Bloom filter negatives per second
    scansPerSec?: number;          // Range scans per second
    pointLookupsPerSec?: number;   // Point lookups (cache miss) per second
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
    activeCompactions?: number; // Count of currently scheduled/pending compactions
    activeCompactionInfos?: ActiveCompactionInfo[]; // Detailed compaction info
    numImmutableMemtables?: number; // Number of immutable memtables waiting to flush
    immutableMemtableSizesMB?: number[]; // Sizes of immutable memtables waiting to flush
    baseLevel?: number; // Base level for universal compaction and leveled compaction with dynamic level bytes (lowest non-empty level below L0)
    currentIncomingRateMBps?: number; // Current incoming write rate (for advanced traffic models, shows actual current rate)
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
    | { type: 'reset_config' }
    | { type: 'status'; running: boolean; config: SimulationConfig }
    | { type: 'metrics'; metrics: SimulationMetrics }
    | { type: 'state'; state: SimulationState }
    | { type: 'event'; event: SimulationEvent }
    | { type: 'log'; log: string }
    | { type: 'error'; error: string };

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

