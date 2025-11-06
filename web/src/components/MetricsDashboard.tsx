import { TrendingUp, Activity, Database, Clock, ArrowDown } from 'lucide-react';
import { useStore } from '../store';

export function MetricsDashboard() {
    const { currentMetrics, currentState, config } = useStore();
    
    // Get incoming write rate from state (current rate) or config (fallback)
    const getIncomingRate = () => {
        // Prefer current rate from state (for advanced models, this shows actual current rate)
        const currentRate = currentState?.currentIncomingRateMBps;
        if (currentRate !== undefined && currentRate !== null) {
            if (!config?.trafficDistribution || config.trafficDistribution.model === 'constant') {
                return { rate: currentRate, isVariable: false };
            } else {
                // Advanced model: show current rate, but also show base/burst info
                const traffic = config.trafficDistribution;
                const baseRate = traffic.baseRateMBps || 0;
                const burstRate = baseRate * (traffic.burstMultiplier || 1.0);
                return { 
                    rate: currentRate, 
                    isVariable: true,
                    burstRate: burstRate,
                    baseRate: baseRate,
                };
            }
        }
        
        // Fallback to config if state doesn't have current rate
        if (!config?.trafficDistribution) {
            return { rate: config?.writeRateMBps || 0, isVariable: false };
        }
        const traffic = config.trafficDistribution;
        if (traffic.model === 'constant') {
            return { rate: traffic.writeRateMBps || 0, isVariable: false };
        } else {
            // Advanced model: show base rate, but indicate it's variable
            const baseRate = traffic.baseRateMBps || 0;
            const burstRate = baseRate * (traffic.burstMultiplier || 1.0);
            return { 
                rate: baseRate, 
                isVariable: true,
                burstRate: burstRate,
                baseRate: baseRate,
            };
        }
    };
    
    const incomingRateInfo = getIncomingRate();

    const formatTime = (seconds: number) => {
        if (seconds < 60) return `${seconds.toFixed(1)}s`;
        if (seconds < 3600) return `${(seconds / 60).toFixed(1)}m`;
        return `${(seconds / 3600).toFixed(1)}h`;
    };

    const formatBytes = (mb: number) => {
        if (mb < 1024) return `${mb.toFixed(1)} MB`;
        if (mb < 1024 * 1024) return `${(mb / 1024).toFixed(1)} GB`;
        return `${(mb / (1024 * 1024)).toFixed(1)} TB`;
    };

    // Count total active compactions across all levels
    const activeCompactionCount = currentState?.activeCompactions?.length ?? 0;

    return (
        <div className="space-y-6">
            {/* Key Metrics Cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-7 gap-4">
                {/* Incoming Write Rate */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <ArrowDown className="w-4 h-4 text-cyan-400" />
                            <span className="text-sm text-gray-400">Incoming Rate</span>
                        </div>
                    </div>
                    <div className="text-3xl font-bold text-cyan-400">
                        {incomingRateInfo.rate.toFixed(1)} MB/s
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {incomingRateInfo.isVariable ? (
                            <>
                                <div>Base: {incomingRateInfo.baseRate?.toFixed(1) || '0'} MB/s</div>
                                <div>Burst: {incomingRateInfo.burstRate?.toFixed(1) || '0'} MB/s</div>
                                <div className="text-cyan-400 mt-0.5">Variable (ON/OFF)</div>
                            </>
                        ) : (
                            <div>Constant rate</div>
                        )}
                    </div>
                </div>
                
                {/* Active Compactions */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <Activity className={`w-4 h-4 ${activeCompactionCount > 0 ? 'text-yellow-400 animate-pulse' : 'text-gray-500'}`} />
                            <span className="text-sm text-gray-400">Active Compactions</span>
                        </div>
                    </div>
                    <div className={`text-3xl font-bold ${activeCompactionCount > 0 ? 'text-yellow-400' : 'text-gray-500'}`}>
                        {activeCompactionCount}
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {activeCompactionCount > 0 ? `${config.maxBackgroundJobs} max parallel` : 'Idle'}
                    </div>
                </div>

                {/* Write Stall Status */}
                <div className={`bg-dark-card border ${currentMetrics?.isOOMKilled ? 'border-red-600' : currentMetrics?.isStalled ? 'border-red-500' : 'border-dark-border'} rounded-lg p-4 shadow-lg ${currentMetrics?.isOOMKilled ? 'bg-red-900/20' : ''}`}>
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <Activity className={`w-4 h-4 ${currentMetrics?.isOOMKilled ? 'text-red-600 animate-pulse' : currentMetrics?.isStalled ? 'text-red-400 animate-pulse' : 'text-green-400'}`} />
                            <span className="text-sm text-gray-400">Write Stall Status</span>
                        </div>
                    </div>
                    {currentMetrics?.isOOMKilled ? (
                        <>
                            <div className="text-3xl font-bold text-red-600 animate-pulse">
                                OOM KILLED
                            </div>
                            <div className="text-xs text-red-400 mt-1 space-y-1">
                                <div className="font-bold">Simulation stopped: Out of Memory</div>
                                <div className="text-gray-400">
                                    Stalled write backlog exceeded limit ({config?.maxStalledWriteMemoryMB || 4096} MB)
                                </div>
                                <div className="text-gray-500 mt-1">
                                    {currentMetrics.stalledWriteCount || 0} writes queued
                                    {config && (
                                        <span className="ml-1">
                                            ({formatBytes((currentMetrics.stalledWriteCount || 0) * 1)})
                                        </span>
                                    )}
                                </div>
                            </div>
                        </>
                    ) : (
                        <>
                            <div className={`text-3xl font-bold ${currentMetrics?.isStalled ? 'text-red-400' : 'text-green-400'}`}>
                                {currentMetrics?.isStalled ? 'STALLED' : 'NORMAL'}
                            </div>
                            <div className="text-xs text-gray-500 mt-1 space-y-1">
                                {currentMetrics?.isStalled ? (
                                    <div className="text-red-400 font-medium">
                                        {currentMetrics.stalledWriteCount || 0} writes queued
                                        {config && (
                                            <span className="text-gray-400 ml-1">
                                                ({formatBytes((currentMetrics.stalledWriteCount || 0) * 1)})
                                            </span>
                                        )}
                                    </div>
                                ) : (
                                    <div>Writes flowing normally</div>
                                )}
                                {/* Always show cumulative metrics if they exist */}
                                {(currentMetrics?.maxStalledWriteCount && currentMetrics.maxStalledWriteCount > 0) ||
                                    (currentMetrics?.stallDurationSeconds && currentMetrics.stallDurationSeconds > 0) ? (
                                    <div className="mt-1 space-y-0.5 text-gray-400 border-t border-dark-border pt-1">
                                        {currentMetrics.maxStalledWriteCount && currentMetrics.maxStalledWriteCount > 0 && config && (
                                            <div>
                                                Peak: {currentMetrics.maxStalledWriteCount} writes
                                                ({formatBytes(currentMetrics.maxStalledWriteCount * 1)})
                                            </div>
                                        )}
                                        {currentMetrics.stallDurationSeconds && currentMetrics.stallDurationSeconds > 0 && (
                                            <div>Total stalled: {formatTime(currentMetrics.stallDurationSeconds)}</div>
                                        )}
                                    </div>
                                ) : null}
                            </div>
                        </>
                    )}
                </div>

                {/* Write Amplification */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <TrendingUp className="w-4 h-4 text-orange-400" />
                            <span className="text-sm text-gray-400">Write Amplification</span>
                        </div>
                    </div>
                    <div className="text-3xl font-bold text-orange-400">
                        {currentMetrics?.writeAmplification.toFixed(2) ?? '--'}×
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {currentMetrics && `${formatBytes(currentMetrics.totalDataWrittenMB)} written`}
                    </div>
                </div>

                {/* Read Amplification */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <Activity className="w-4 h-4 text-blue-400" />
                            <span className="text-sm text-gray-400">Read Amplification</span>
                        </div>
                    </div>
                    <div className="text-3xl font-bold text-blue-400">
                        {currentMetrics?.readAmplification.toFixed(2) ?? '--'}×
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {currentMetrics && `${formatBytes(currentMetrics.totalDataReadMB)} read`}
                    </div>
                </div>

                {/* Space Amplification */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <Database className="w-4 h-4 text-purple-400" />
                            <span className="text-sm text-gray-400">Space Amplification</span>
                        </div>
                    </div>
                    <div className="text-3xl font-bold text-purple-400">
                        {currentMetrics?.spaceAmplification.toFixed(2) ?? '--'}×
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {currentState && `${formatBytes(currentState.totalSizeMB)} total`}
                    </div>
                </div>

                {/* Virtual Time */}
                <div className="bg-dark-card border border-dark-border rounded-lg p-4 shadow-lg">
                    <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                            <Clock className="w-4 h-4 text-green-400" />
                            <span className="text-sm text-gray-400">Virtual Time</span>
                        </div>
                    </div>
                    <div className="text-3xl font-bold text-green-400">
                        {currentState ? formatTime(currentState.virtualTime) : '--'}
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                        {currentState && (
                            <>
                                <div>{currentState.memtableCurrentSizeMB.toFixed(1)} MB in memtable</div>
                                {currentMetrics?.isStalled && (
                                    <div className="text-red-400 mt-1">
                                        {currentState.numImmutableMemtables || 0}/{config.maxWriteBufferNumber} immutable
                                    </div>
                                )}
                            </>
                        )}
                    </div>
                </div>
            </div>

            {/* Throughput Metrics - Values Only (graphs disabled to prevent browser memory issues) */}
            <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-lg">
                <div className="flex items-center justify-between mb-4">
                    <h3 className="text-lg font-semibold flex items-center gap-2">
                        <Activity className="w-5 h-5 text-purple-400" />
                        Write Throughput (MB/s)
                    </h3>
                    <div className="flex items-center gap-4">
                        {currentMetrics?.inProgressCount && currentMetrics.inProgressCount > 0 && (
                            <div className="text-sm text-yellow-400 flex items-center gap-2">
                                <span className="animate-pulse">●</span>
                                {currentMetrics.inProgressCount} active {currentMetrics.inProgressCount === 1 ? 'write' : 'writes'}
                            </div>
                        )}
                        {currentState?.activeCompactions && currentState.activeCompactions.length > 0 && (
                            <div className="text-sm text-gray-400">
                                Compacting: {currentState.activeCompactions.map(l => `L${l}`).join(', ')}
                            </div>
                        )}
                    </div>
                </div>

                {/* In-Progress Activities */}
                {currentMetrics?.inProgressDetails && currentMetrics.inProgressDetails.length > 0 && (
                    <div className="mb-4 p-3 bg-yellow-900/20 border border-yellow-600/30 rounded-lg">
                        <div className="text-xs font-semibold text-yellow-400 mb-2">Active I/O Operations:</div>
                        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
                            {currentMetrics.inProgressDetails.map((detail, idx) => (
                                <div key={idx} className="text-xs text-gray-300 font-mono bg-dark-bg/50 p-2 rounded">
                                    {detail.fromLevel === -1 ? (
                                        <>Flush → L0: {detail.outputMB.toFixed(1)} MB</>
                                    ) : (
                                        <>L{detail.fromLevel}→L{detail.toLevel}: {detail.inputMB.toFixed(1)} MB → {detail.outputMB.toFixed(1)} MB</>
                                    )}
                                </div>
                            ))}
                        </div>
                    </div>
                )}
                <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-4">
                    {/* Flush Throughput */}
                    <div className="bg-dark-bg/50 rounded-lg p-3 border border-gray-700">
                        <div className="text-xs text-gray-400 mb-1">Flush (L0)</div>
                        <div className="text-2xl font-bold text-green-400">
                            {currentMetrics?.flushThroughputMBps?.toFixed(1) ?? '0.0'}
                        </div>
                    </div>

                    {/* Per-Level Compaction Throughput */}
                    {config && currentMetrics?.perLevelThroughputMBps &&
                        Array.from({ length: config.numLevels - 1 }, (_, idx) => {
                            const throughput = currentMetrics.perLevelThroughputMBps[idx] || 0;
                            const levelColors = ['text-amber-400', 'text-red-400', 'text-pink-400', 'text-purple-400', 'text-indigo-400', 'text-blue-400', 'text-cyan-400'];
                            const colorClass = levelColors[idx % levelColors.length];
                            
                            // For universal compaction, try to determine actual target level from inProgressDetails
                            let targetLevelLabel = idx + 1;
                            if (config.compactionStyle === 'universal' && currentMetrics.inProgressDetails) {
                                // Find the most recent compaction from this level
                                const compactionFromLevel = currentMetrics.inProgressDetails
                                    .filter(d => d.fromLevel === idx)
                                    .sort((a, b) => b.inputMB - a.inputMB)[0]; // Use largest one as most representative
                                if (compactionFromLevel) {
                                    targetLevelLabel = compactionFromLevel.toLevel;
                                } else if (currentState?.baseLevel !== undefined && idx === 0) {
                                    // For L0, use base level if available
                                    targetLevelLabel = currentState.baseLevel;
                                }
                            }

                            return (
                                <div key={`level${idx}`} className="bg-dark-bg/50 rounded-lg p-3 border border-gray-700">
                                    <div className="text-xs text-gray-400 mb-1">L{idx}→L{targetLevelLabel}</div>
                                    <div className={`text-2xl font-bold ${colorClass}`}>
                                        {throughput.toFixed(1)}
                                    </div>
                                </div>
                            );
                        })
                    }

                    {/* Total Throughput */}
                    <div className="bg-dark-bg/50 rounded-lg p-3 border-2 border-purple-600">
                        <div className="text-xs text-gray-400 mb-1">Total</div>
                        <div className="text-2xl font-bold text-white">
                            {currentMetrics?.totalWriteThroughputMBps?.toFixed(1) ?? '0.0'}
                        </div>
                        <div className="text-xs text-gray-500 mt-1">
                            Limit: {config?.ioThroughputMBps ?? '--'} MB/s
                        </div>
                    </div>
                </div>
            </div>

            {/* Charts - TEMPORARILY DISABLED to prevent browser memory issues
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            Amplification Chart
            <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-lg">
                    <h3 className="text-lg font-semibold mb-4">Amplification Factors</h3>
                    <ResponsiveContainer width="100%" height={300}>
                        <LineChart data={metricsHistory}>
                            <CartesianGrid strokeDasharray="3 3" stroke="#2a2a3e" />
                            <XAxis
                                dataKey="timestamp"
                                stroke="#666"
                                tickFormatter={(t) => formatTime(t)}
                            />
                            <YAxis stroke="#666" />
                            <Tooltip
                                contentStyle={{
                                    backgroundColor: '#1a1a2e',
                                    border: '1px solid #2a2a3e',
                                    borderRadius: '8px'
                                }}
                                labelFormatter={(t) => `Time: ${formatTime(t as number)}`}
                            />
                            <Legend />
                            <Line
                                type="monotone"
                                dataKey="writeAmplification"
                                stroke="#fb923c"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Write Amp"
                            />
                            <Line
                                type="monotone"
                                dataKey="readAmplification"
                                stroke="#60a5fa"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Read Amp"
                            />
                            <Line
                                type="monotone"
                                dataKey="spaceAmplification"
                                stroke="#c084fc"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Space Amp"
                            />
                        </LineChart>
                    </ResponsiveContainer>
                </div>

                Latency Chart
                <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-lg">
                    <h3 className="text-lg font-semibold mb-4">Latencies</h3>
                    <ResponsiveContainer width="100%" height={300}>
                        <LineChart data={metricsHistory}>
                            <CartesianGrid strokeDasharray="3 3" stroke="#2a2a3e" />
                            <XAxis
                                dataKey="timestamp"
                                stroke="#666"
                                tickFormatter={(t) => formatTime(t)}
                            />
                            <YAxis stroke="#666" label={{ value: 'ms', angle: -90, position: 'insideLeft' }} />
                            <Tooltip
                                contentStyle={{
                                    backgroundColor: '#1a1a2e',
                                    border: '1px solid #2a2a3e',
                                    borderRadius: '8px'
                                }}
                                labelFormatter={(t) => `Time: ${formatTime(t as number)}`}
                            />
                            <Legend />
                            <Line
                                type="monotone"
                                dataKey="writeLatencyMs"
                                stroke="#fb923c"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Write Latency"
                            />
                        </LineChart>
                    </ResponsiveContainer>
                </div>
            </div> */}
        </div>
    );
}
