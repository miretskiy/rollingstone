import { Database, Activity } from 'lucide-react';
import { useStore } from '../store';
import type { LevelState } from '../types';

interface CompactionDetail {
    inputMB: number;
    outputMB: number;
    fromLevel: number;
    toLevel: number;
}

interface LevelProps {
    level: LevelState;
    isCompacting: boolean;
    compactionDetails?: CompactionDetail[];
}

function Level({ level, isCompacting, compactionDetails }: LevelProps) {
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
                                {level.level === 0 ? 'L0 (Tiered)' : `L${level.level} (Leveled)`}
                            </span>
                            {isCompacting && compactionDetails && compactionDetails.length > 0 && (
                                <span className="flex items-center gap-1 text-xs text-yellow-400">
                                    <Activity className="w-3 h-3 animate-pulse" />
                                    Compacting: {compactionDetails.map(d =>
                                        `${formatSize(d.inputMB)} → ${formatSize(d.outputMB)}`
                                    ).join(', ')}
                                </span>
                            )}
                            {isCompacting && (!compactionDetails || compactionDetails.length === 0) && (
                                <span className="flex items-center gap-1 text-xs text-yellow-400">
                                    <Activity className="w-3 h-3 animate-pulse" />
                                    Compacting
                                </span>
                            )}
                        </div>
                        <div className="flex items-center gap-4 text-sm text-gray-400 mt-1">
                            <span>{level.fileCount} {level.fileCount === 1 ? 'file' : 'files'}</span>
                            <span>•</span>
                            <span>{formatSize(level.totalSizeMB)}</span>
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
    const { currentState, currentMetrics } = useStore();

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

    const activeCompactions = new Set(currentState.activeCompactions || []);

    // Build a map of level -> compaction details
    const compactionsByLevel = new Map<number, CompactionDetail[]>();
    if (currentMetrics?.inProgressDetails) {
        for (const detail of currentMetrics.inProgressDetails) {
            const fromLevel = detail.fromLevel;
            if (!compactionsByLevel.has(fromLevel)) {
                compactionsByLevel.set(fromLevel, []);
            }
            compactionsByLevel.get(fromLevel)!.push(detail);
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
                <div className="flex items-center gap-3">
                    <Database className="w-5 h-5 text-green-400" />
                    <div className="flex-1">
                        <div className="text-lg font-bold text-green-400">Memtable (In-Memory)</div>
                        <div className="text-sm text-gray-400">
                            {formatSize(currentState.memtableCurrentSizeMB)}
                        </div>
                    </div>
                    <div className="w-48 h-6 bg-dark-bg rounded-full overflow-hidden">
                        <div
                            className="h-full bg-green-500 rounded-full transition-all duration-300"
                            style={{ width: `${(currentState.memtableCurrentSizeMB / 128) * 100}%` }}
                        />
                    </div>
                </div>
            </div>

            {/* Levels */}
            <div className="space-y-3">
                {currentState.levels.map((level) => (
                    <Level
                        key={level.level}
                        level={level}
                        isCompacting={activeCompactions.has(level.level)}
                        compactionDetails={compactionsByLevel.get(level.level)}
                    />
                ))}
            </div>
        </div>
    );
}

