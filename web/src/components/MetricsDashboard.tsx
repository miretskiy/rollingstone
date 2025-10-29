import { TrendingUp, Activity, Database, Clock } from 'lucide-react';
import { useStore } from '../store';

export function MetricsDashboard() {
    const { currentMetrics, currentState, config } = useStore();

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
    const activeCompactionCount = currentState?.activeCompactionInfos?.length ?? 0;

    return (
        <div className="space-y-6">
            {/* Key Metrics Cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
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
                        {currentState && `${currentState.memtableCurrentSizeMB.toFixed(1)} MB in memtable`}
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

                            return (
                                <div key={`level${idx}`} className="bg-dark-bg/50 rounded-lg p-3 border border-gray-700">
                                    <div className="text-xs text-gray-400 mb-1">L{idx}→L{idx + 1}</div>
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
                            <Line
                                type="monotone"
                                dataKey="readLatencyMs"
                                stroke="#60a5fa"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Read Latency"
                            />
                        </LineChart>
                    </ResponsiveContainer>
                </div>

                Write Throughput Chart
                <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-lg">
                    <h3 className="text-lg font-semibold mb-4">Write Throughput (MB/s)</h3>
                    <ResponsiveContainer width="100%" height={300}>
                        <LineChart data={throughputData}>
                            <CartesianGrid strokeDasharray="3 3" stroke="#2a2a3e" />
                            <XAxis
                                dataKey="timestamp"
                                stroke="#666"
                                tickFormatter={(t) => formatTime(t)}
                            />
                            <YAxis stroke="#666" label={{ value: 'MB/s', angle: -90, position: 'insideLeft' }} />
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
                                dataKey="flushThroughputMBps"
                                stroke="#10b981"
                                strokeWidth={2}
                                dot={false}
                                isAnimationActive={false}
                                name="Flush (L0)"
                            />
                            Render per-level compaction lines based on configured levels
                            {config && (() => {
                                const levelColors = ['#f59e0b', '#ef4444', '#ec4899', '#a855f7', '#6366f1', '#3b82f6', '#06b6d4'];
                                // Generate lines for L0→L1, L1→L2, ..., L(n-2)→L(n-1)
                                return Array.from({ length: config.numLevels - 1 }, (_, idx) => (
                                    <Line
                                        key={`level${idx}`}
                                        type="monotone"
                                        dataKey={`level${idx}ThroughputMBps`}
                                        stroke={levelColors[idx % levelColors.length]}
                                        strokeWidth={2}
                                        dot={false}
                                        isAnimationActive={false}
                                        name={`L${idx}→L${idx + 1}`}
                                    />
                                ));
                            })()}
                            <Line
                                type="monotone"
                                dataKey="totalWriteThroughputMBps"
                                stroke="#ffffff"
                                strokeWidth={3}
                                dot={false}
                                isAnimationActive={false}
                                name="Total"
                                strokeDasharray="5 5"
                            />
                        </LineChart>
                    </ResponsiveContainer>
                </div>
            </div> */}
        </div>
    );
}

