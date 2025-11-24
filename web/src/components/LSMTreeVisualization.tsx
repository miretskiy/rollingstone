import { Database, Activity, Layers } from 'lucide-react';
import { useStore } from '../store';
import type { LevelState, ActiveCompactionInfo, CompactionStats } from '../types';

interface LevelProps {
    level: LevelState;
    compactionInfos: ActiveCompactionInfo[];
    compactionsSinceUpdate?: CompactionStats;
    baseLevel?: number; // Base level for universal compaction and leveled compaction with dynamic level bytes (lowest non-empty level below L0)
}

function Level({ level, compactionInfos, compactionsSinceUpdate, baseLevel }: LevelProps) {
    const isCompacting = compactionInfos.length > 0;
    const config = useStore(state => state.config);
    const formatSize = (mb: number) => {
        if (mb < 1024) return `${mb.toFixed(1)} MB`;
        if (mb < 1024 * 1024) return `${(mb / 1024).toFixed(1)} GB`;
        return `${(mb / (1024 * 1024)).toFixed(1)} TB`;
    };

    // Calculate width percentage based on size (logarithmic scale for better visualization)
    const getWidthPercentage = () => {
        if (level.totalSizeMB === 0) return 0;
        // Use log scale to better visualize size differences
        const logSize = Math.log10(level.totalSizeMB + 1);
        const maxLogSize = Math.log10(1024 * 1024); // 1 TB
        return Math.min(100, (logSize / maxLogSize) * 100);
    };

    const widthPercentage = getWidthPercentage();

    // Color gradient from blue (small) to purple (large)
    const getLevelColor = () => {
        const intensity = Math.min(widthPercentage / 100, 1);
        const hue = 220 - intensity * 40; // 220 (blue) to 180 (cyan)
        return `hsl(${hue}, 70%, 50%)`;
    };

    // Find oldest file timestamp
    const oldestFileTimestamp = level.files.length > 0
        ? Math.max(...level.files.map(f => f.ageSeconds))
        : 0;

    const formatAge = (seconds: number) => {
        if (seconds === 0) return 'N/A';
        if (seconds < 60) return `${seconds.toFixed(1)}s`;
        if (seconds < 3600) return `${(seconds / 60).toFixed(1)}m`;
        if (seconds < 86400) return `${(seconds / 3600).toFixed(1)}h`;
        return `${(seconds / 86400).toFixed(1)}d`;
    };

    return (
        <div className={`border rounded-lg p-4 bg-dark-card transition-colors ${isCompacting ? 'border-yellow-600 bg-yellow-900/10' : 'border-dark-border'}`}>
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-3 flex-1">
                    <Database className="w-5 h-5" style={{ color: getLevelColor() }} />
                    <div className="flex-1">
                        <div className="flex items-center gap-2">
                            <span className="text-lg font-bold">
                                {config?.compactionStyle === 'fifo'
                                    ? 'L0 (FIFO - Single Level)'
                                    : level.level === 0 ? 'L0 (Tiered)' : `L${level.level} (Leveled)`
                                }
                            </span>
                            {baseLevel !== undefined && level.level === baseLevel && (
                                <span className="text-xs px-2 py-0.5 bg-purple-600/30 text-purple-300 border border-purple-500 rounded font-semibold" title="Base level: lowest non-empty level below L0. In universal compaction, files below base level are never compacted. In leveled compaction with dynamic level bytes, L0 compacts directly to base level, skipping empty intermediate levels.">
                                    BASE
                                </span>
                            )}
                            {isCompacting && (() => {
                                // Aggregate file counts for all compactions
                                const totalSourceFiles = compactionInfos.reduce((sum, c) => sum + c.sourceFileCount, 0);
                                const totalTargetFiles = compactionInfos.reduce((sum, c) => sum + c.targetFileCount, 0);
                                const isIntraL0 = compactionInfos[0].isIntraL0;
                                const toLevel = compactionInfos[0].toLevel;
                                const count = compactionInfos.length;

                                return (
                                    <span className="flex items-center gap-1 text-xs text-yellow-400">
                                        <Activity className="w-3 h-3 animate-pulse" />
                                        {isIntraL0 ?
                                            `Intra-L0: ${totalSourceFiles} files` :
                                            `→L${toLevel}: ${totalSourceFiles} + ${totalTargetFiles} files`
                                        }
                                        {count > 1 && (
                                            <span className="flex items-center gap-0.5 ml-1">
                                                (<Layers className="w-3 h-3" />{count})
                                            </span>
                                        )}
                                    </span>
                                );
                            })()}
                        </div>
                        {/* Compactions completed since last UI update (for fast simulations) */}
                        {compactionsSinceUpdate && compactionsSinceUpdate.count > 0 && (
                            <div className="mt-1 text-xs text-blue-400">
                                ↻ {compactionsSinceUpdate.count} compaction{compactionsSinceUpdate.count > 1 ? 's' : ''} since last update
                                ({compactionsSinceUpdate.totalInputFiles} → {compactionsSinceUpdate.totalOutputFiles} files)
                            </div>
                        )}
                        <div className="flex items-center gap-4 text-sm text-gray-400 mt-1">
                            <span>{level.fileCount} {level.fileCount === 1 ? 'file' : 'files'}</span>
                            <span>•</span>
                            <span>
                                {formatSize(level.totalSizeMB)}
                                {level.level > 0 && level.targetSizeMB !== undefined && level.targetSizeMB > 0 && (
                                    <span className="text-gray-500">
                                        {' '}/ {formatSize(level.targetSizeMB)} target
                                    </span>
                                )}
                            </span>
                            {level.level > 0 && level.targetSizeMB !== undefined && level.targetSizeMB > 0 && (
                                <>
                                    <span>•</span>
                                    <span className={level.totalSizeMB > level.targetSizeMB ? 'text-yellow-400' : 'text-green-400'}>
                                        {(level.totalSizeMB / level.targetSizeMB).toFixed(2)}x
                                    </span>
                                </>
                            )}
                            {oldestFileTimestamp > 0 && (
                                <>
                                    <span>•</span>
                                    <span>Oldest: {formatAge(oldestFileTimestamp)}</span>
                                </>
                            )}
                        </div>
                    </div>
                </div>

                {/* Visual Size Indicator */}
                <div className="w-48 h-6 bg-dark-bg rounded-full overflow-hidden ml-4">
                    <div
                        className="h-full rounded-full transition-all duration-300"
                        style={{
                            width: `${widthPercentage}%`,
                            backgroundColor: getLevelColor(),
                        }}
                    />
                </div>
            </div>
        </div>
    );
}

export function LSMTreeVisualization() {
    const { currentState, currentMetrics, config } = useStore();

    const formatSize = (mb: number) => {
        if (mb < 1024) return `${mb.toFixed(1)} MB`;
        if (mb < 1024 * 1024) return `${(mb / 1024).toFixed(1)} GB`;
        return `${(mb / (1024 * 1024)).toFixed(1)} TB`;
    };

    if (!currentState) {
        return (
            <div className="bg-dark-card border border-dark-border rounded-lg p-12 text-center">
                <Database className="w-16 h-16 text-gray-600 mx-auto mb-4" />
                <p className="text-gray-500">No simulation data yet. Start the simulation to see the LSM tree.</p>
            </div>
        );
    }

    // Build a map of level -> active compaction infos
    const compactionInfosByLevel = new Map<number, ActiveCompactionInfo[]>();
    if (currentState.activeCompactionInfos) {
        for (const info of currentState.activeCompactionInfos) {
            const fromLevel = info.fromLevel;
            if (!compactionInfosByLevel.has(fromLevel)) {
                compactionInfosByLevel.set(fromLevel, []);
            }
            compactionInfosByLevel.get(fromLevel)!.push(info);
        }
    }

    return (
        <div className="space-y-4">
            {/* Header with Summary */}
            <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-lg">
                <div>
                    <h2 className="text-2xl font-bold mb-2">LSM Tree Structure</h2>
                    <p className="text-gray-400">
                        Total Size: <span className="text-primary-400 font-bold">{formatSize(currentState.totalSizeMB)}</span>
                        {' • '}
                        Memtable: <span className="text-green-400 font-bold">{formatSize(currentState.memtableCurrentSizeMB)}</span>
                    </p>
                </div>
            </div>

            {/* Memtable */}
            <div className="bg-dark-card border border-green-900 rounded-lg p-4 shadow-lg">
                <div className="flex items-center gap-3 mb-3">
                    <Database className="w-5 h-5 text-green-400" />
                    <div className="flex-1">
                        <div className="text-lg font-bold text-green-400">Memtable (In-Memory)</div>
                        <div className="text-sm text-gray-400">
                            Active: {formatSize(currentState.memtableCurrentSizeMB)}
                            {config && ` / ${formatSize(config.memtableFlushSizeMB)}`}
                        </div>
                    </div>
                    <div className="w-48 h-6 bg-dark-bg rounded-full overflow-hidden">
                        <div
                            className="h-full bg-green-500 rounded-full transition-all duration-300"
                            style={{ 
                                width: `${config ? Math.min(100, (currentState.memtableCurrentSizeMB / config.memtableFlushSizeMB) * 100) : 0}%` 
                            }}
                        />
                    </div>
                </div>
                {/* Immutable Memtables */}
                {currentState.numImmutableMemtables && currentState.numImmutableMemtables > 0 && (
                    <div className="mt-2 pt-2 border-t border-green-900/30">
                        <div className="flex items-center justify-between text-sm">
                            <span className={`${currentState.numImmutableMemtables >= (config?.maxWriteBufferNumber || 2) ? 'text-red-400 font-bold' : 'text-yellow-400'}`}>
                                ⏳ Immutable: {currentState.numImmutableMemtables} memtable{currentState.numImmutableMemtables > 1 ? 's' : ''} flushing
                                {config && currentState.numImmutableMemtables >= config.maxWriteBufferNumber && (
                                    <span className="ml-1">(STALLED)</span>
                                )}
                            </span>
                            <span className="text-yellow-300 font-mono">
                                {formatSize((currentState.immutableMemtableSizesMB || []).reduce((a, b) => a + b, 0))}
                            </span>
                        </div>
                    </div>
                )}
            </div>

            {/* Levels */}
            <div className="space-y-3">
                {currentState.levels
                    .filter(level => {
                        // For FIFO compaction, only show L0 (level 0)
                        if (config?.compactionStyle === 'fifo') {
                            return level.level === 0;
                        }
                        // For other compaction styles, show all levels
                        return true;
                    })
                    .map((level) => (
                        <Level
                            key={level.level}
                            level={level}
                            compactionInfos={compactionInfosByLevel.get(level.level) || []}
                            compactionsSinceUpdate={currentMetrics?.compactionsSinceUpdate?.[level.level]}
                            baseLevel={currentState.baseLevel}
                        />
                    ))}
            </div>
        </div>
    );
}

