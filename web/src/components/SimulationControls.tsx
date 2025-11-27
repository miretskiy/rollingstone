import { useState, useEffect } from 'react';
import { Play, Pause, RotateCcw, Settings, ChevronDown, ChevronRight, AlertTriangle, HelpCircle, RefreshCw } from 'lucide-react';
import { useStore } from '../store';
import type { SimulationConfig, ReadWorkloadConfig } from '../types';
import { ConfigInput } from './ConfigInput';

// Helper component for number inputs with local state (allows editing)
function NumberInput({
  value,
  onChange,
  min,
  max,
  disabled,
  className,
}: {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  disabled?: boolean;
  className?: string;
}) {
  const [localValue, setLocalValue] = useState(String(value));
  const [isFocused, setIsFocused] = useState(false);

  // Sync when not focused
  useEffect(() => {
    if (!isFocused) {
      setLocalValue(String(value));
    }
  }, [value, isFocused]);

  const applyValue = () => {
    const num = parseFloat(localValue);
    if (!isNaN(num)) {
      let clamped = num;
      if (min !== undefined) clamped = Math.max(min, clamped);
      if (max !== undefined) clamped = Math.min(max, clamped);
      onChange(clamped);
      setLocalValue(String(clamped));
    } else {
      setLocalValue(String(value));
    }
  };

  return (
    <input
      type="text"
      inputMode="decimal"
      value={localValue}
      onChange={(e) => setLocalValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          applyValue();
          e.currentTarget.blur();
        }
      }}
      onFocus={(e) => {
        e.target.select();
        setIsFocused(true);
      }}
      onBlur={() => {
        setIsFocused(false);
        applyValue();
      }}
      disabled={disabled}
      className={className}
    />
  );
}

export function SimulationControls() {
  const { connectionStatus, isRunning, start, pause, reset, resetConfig, updateConfig } = useStore();
  // Read current config values
  const ioLatency = useStore(state => state.config.ioLatencyMs);
  const ioThroughput = useStore(state => state.config.ioThroughputMBps);
  const writeRate = useStore(state => state.config.writeRateMBps);
  const currentMetrics = useStore(state => state.currentMetrics);
  const maxBackgroundJobs = useStore(state => state.config.maxBackgroundJobs);
  const bufferCapacityMB = useStore(state => state.config.maxStalledWriteMemoryMB) || 4096;
  const compactionStyle = useStore(state => state.config.compactionStyle) || 'universal';
  const levelCompactionDynamicLevelBytes = useStore(state => state.config.levelCompactionDynamicLevelBytes) || false;
  const fifoAllowCompaction = useStore(state => state.config.fifoAllowCompaction) || false;
  const overlapDistTypeRaw = useStore(state => state.config.overlapDistribution?.type);
  const overlapDistType = (overlapDistTypeRaw === 'uniform' || overlapDistTypeRaw === 'exponential' || overlapDistTypeRaw === 'geometric' || overlapDistTypeRaw === 'fixed') 
    ? overlapDistTypeRaw 
    : 'geometric'; // Default to geometric if invalid or missing
  
  // Extract all traffic distribution values (must be at top level for hooks)
  const trafficDist = useStore(state => state.config.trafficDistribution);
  // const baseRateMBps = trafficDist?.baseRateMBps || 10.0; // Will be needed for Advanced Traffic Parameters
  const burstMultiplier = trafficDist?.burstMultiplier || 0;
  const lognormalSigma = trafficDist?.lognormalSigma || 0.1;
  const onMeanSeconds = trafficDist?.onMeanSeconds || 5.0;
  const offMeanSeconds = trafficDist?.offMeanSeconds || 10.0;
  const erlangK = trafficDist?.erlangK || 2;
  const spikeRatePerSec = trafficDist?.spikeRatePerSec || 0.1;
  const spikeMeanDur = trafficDist?.spikeMeanDur || 1.0;
  const spikeAmplitudeMean = trafficDist?.spikeAmplitudeMean || 1.0;
  const spikeAmplitudeSigma = trafficDist?.spikeAmplitudeSigma || 0.5;

  // Helper function to ensure all required ReadWorkloadConfig fields are present
  const getCompleteReadWorkload = (partial: Partial<ReadWorkloadConfig> = {}): ReadWorkloadConfig => {
    return {
      enabled: true,
      requestsPerSec: partial.requestsPerSec ?? readWorkload?.requestsPerSec ?? 1000,
      cacheHitRate: partial.cacheHitRate ?? readWorkload?.cacheHitRate ?? 0.90,
      bloomNegativeRate: partial.bloomNegativeRate ?? readWorkload?.bloomNegativeRate ?? 0.02,
      scanRate: partial.scanRate ?? readWorkload?.scanRate ?? 0.05,
      cacheHitLatency: partial.cacheHitLatency ?? readWorkload?.cacheHitLatency ?? { distribution: 'fixed', mean: 0.001 },
      bloomNegativeLatency: partial.bloomNegativeLatency ?? readWorkload?.bloomNegativeLatency ?? { distribution: 'fixed', mean: 0.01 },
      pointLookupLatency: partial.pointLookupLatency ?? readWorkload?.pointLookupLatency ?? { distribution: 'exponential', mean: 2.0 },
      scanLatency: partial.scanLatency ?? readWorkload?.scanLatency ?? { distribution: 'lognormal', mean: 10.0 },
      avgScanSizeKB: partial.avgScanSizeKB ?? readWorkload?.avgScanSizeKB ?? 16.0,
    };
  };
  const capacityLimitMB = trafficDist?.capacityLimitMB || 0;

  // WAL configuration (must be at top level for hooks)
  const enableWAL = useStore(state => state.config.enableWAL ?? true);
  const walSync = useStore(state => state.config.walSync ?? true);
  const queueMode = trafficDist?.queueMode || 'drop';

  // Extract all overlap distribution values
  const overlapDist = useStore(state => state.config.overlapDistribution);
  const geometricP = overlapDist?.geometricP || 0.3;
  const exponentialLambda = overlapDist?.exponentialLambda || 0.5;
  const fixedPercentage = overlapDist?.fixedPercentage ?? 0.5; // Default to 0.5 if not set

  // Read workload configuration
  const readWorkload = useStore(state => state.config.readWorkload);

  // Calculate max sustainable rate from config OR from actual metrics if available
  // Use actual metrics if simulation is running and has data, otherwise use theoretical estimate
  let maxSustainableRate: number | undefined;
  let minSustainableRate: number | undefined;
  
  if (currentMetrics?.maxSustainableWriteRateMBps && currentMetrics.maxSustainableWriteRateMBps > 0) {
    // Use actual calculated values from simulation
    maxSustainableRate = currentMetrics.maxSustainableWriteRateMBps;
    minSustainableRate = currentMetrics.minSustainableWriteRateMBps;
  } else {
    // Calculate theoretical estimate from config
    // Adjust based on compaction style
    const baseOverhead = compactionStyle === 'universal' ? 1.8 : 2.5; // Universal: lower write amplification
    const conservativeMultiplier = 3.0;
    const conservativeOverhead = baseOverhead * conservativeMultiplier;
    maxSustainableRate = ioThroughput / (1.0 + conservativeOverhead);
    
    // For worst-case estimate, need to know deepest level and file sizes
    // Use a conservative worst-case estimate (L5→L6 with maxBackgroundJobs compactions)
    const worstCaseFileSizeMB = 1600; // 1.6GB max file size
    const worstCasePerCompactionIO = 4 * worstCaseFileSizeMB; // 4 files per compaction
    const totalWorstCaseIO = worstCasePerCompactionIO * maxBackgroundJobs;
    const worstCaseDuration = totalWorstCaseIO / ioThroughput;
    minSustainableRate = bufferCapacityMB / worstCaseDuration;
  }
  
  // Format range for display (ensure min < max)
  let sustainableRangeStr: string | undefined;
  if (minSustainableRate && maxSustainableRate && minSustainableRate > 0 && maxSustainableRate > 0) {
    // Ensure correct order: min should be lower bound, max should be upper bound
    const min = Math.min(minSustainableRate, maxSustainableRate);
    const max = Math.max(minSustainableRate, maxSustainableRate);
    sustainableRangeStr = `${min.toFixed(0)}-${max.toFixed(0)}`;
  } else if (maxSustainableRate && maxSustainableRate > 0) {
    sustainableRangeStr = maxSustainableRate.toFixed(1);
  }
  
  const isExceedingSustainable = maxSustainableRate !== undefined && maxSustainableRate > 0 && writeRate > maxSustainableRate;

  // Read compression config for preset detection
  const compressionFactor = useStore(state => state.config.compressionFactor ?? 0.85);
  const compressionThroughputMBps = useStore(state => state.config.compressionThroughputMBps ?? 750);
  const decompressionThroughputMBps = useStore(state => state.config.decompressionThroughputMBps ?? 3700);

  // Detect active compression preset
  const getCompressionPreset = (): string => {
    if (Math.abs(compressionFactor - 0.85) < 0.01 && Math.abs(compressionThroughputMBps - 750) < 10 && Math.abs(decompressionThroughputMBps - 3700) < 10) return 'lz4';
    if (Math.abs(compressionFactor - 0.83) < 0.01 && Math.abs(compressionThroughputMBps - 530) < 10 && Math.abs(decompressionThroughputMBps - 1800) < 10) return 'snappy';
    if (Math.abs(compressionFactor - 0.70) < 0.01 && Math.abs(compressionThroughputMBps - 470) < 10 && Math.abs(decompressionThroughputMBps - 1380) < 10) return 'zstd';
    if (Math.abs(compressionFactor - 1.0) < 0.01 && compressionThroughputMBps === 0 && decompressionThroughputMBps === 0) return 'none';
    return 'custom';
  };

  const [expandedSections, setExpandedSections] = useState({
    lsm: true,
    lsmAdvanced: false,
    workload: true,
    workloadAdvanced: false,
    compression: false,
    readWorkload: false,
    readWorkloadAdvanced: false,
    io: false,
    simulation: false,
  });

  const isConnected = connectionStatus === 'connected';

  const toggleSection = (section: keyof typeof expandedSections) => {
    setExpandedSections(prev => ({ ...prev, [section]: !prev[section] }));
  };

  const handleConfigChange = (field: keyof SimulationConfig, value: number) => {
    updateConfig({ [field]: value });
  };

  const PresetButton = ({ label, onClick, disabled, isSelected }: {
    label: string;
    onClick: () => void;
    disabled: boolean;
    isSelected?: boolean;
  }) => (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-2 py-1 text-xs border rounded transition-colors ${isSelected
          ? 'bg-primary-500 border-primary-400 text-white font-bold shadow-lg shadow-primary-500/50'
          : 'bg-dark-bg hover:bg-gray-700 disabled:bg-gray-800 border-dark-border'
        } disabled:cursor-not-allowed`}
    >
      {label}
    </button>
  );

  return (
    <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-xl">
      {/* Header with controls */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-2">
          <Settings className="w-5 h-5 text-primary-400" />
          <h2 className="text-xl font-bold">Simulation Controls</h2>
          <div className="group relative">
            <button
              onClick={resetConfig}
              disabled={!isConnected || isRunning}
              className="p-1.5 bg-dark-bg hover:bg-gray-700 disabled:bg-gray-800 disabled:cursor-not-allowed rounded transition-all transform hover:scale-105 active:scale-95 disabled:opacity-50"
              title={isRunning ? "Cannot reset configuration while simulation is running" : "Reset configuration to default values"}
            >
              <RefreshCw className="w-4 h-4 text-gray-400" />
            </button>
            <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg pointer-events-none">
              {isRunning ? "Cannot reset configuration while simulation is running" : "Reset all configuration values to their defaults"}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <div className="flex items-center gap-2 mr-4">
            <div className={`w-2 h-2 rounded-full ${isConnected ? 'bg-green-500 animate-pulse' : 'bg-gray-500'}`} />
            <span className="text-sm text-gray-400 capitalize">{connectionStatus}</span>
          </div>

          <button
            onClick={isRunning ? pause : start}
            disabled={!isConnected || (currentMetrics?.isOOMKilled)}
            className={`flex items-center gap-2 px-6 py-3 rounded-lg font-semibold transition-all transform hover:scale-105 active:scale-95 ${
              currentMetrics?.isOOMKilled 
                ? 'bg-red-600 cursor-not-allowed' 
                : isRunning 
                  ? 'bg-primary-600 hover:bg-primary-700' 
                  : 'bg-primary-600 hover:bg-primary-700'
            } ${!isConnected ? 'disabled:bg-gray-600 disabled:cursor-not-allowed' : ''}`}
            title={currentMetrics?.isOOMKilled ? 'Simulation OOM killed - cannot resume' : undefined}
          >
            {currentMetrics?.isOOMKilled ? (
              <><AlertTriangle className="w-5 h-5" />OOM Killed</>
            ) : isRunning ? (
              <><Pause className="w-5 h-5" />Pause</>
            ) : (
              <><Play className="w-5 h-5" />Play</>
            )}
          </button>

          <button
            onClick={reset}
            disabled={!isConnected}
            className="p-3 bg-dark-bg hover:bg-gray-700 disabled:bg-gray-800 disabled:cursor-not-allowed rounded-lg transition-all transform hover:scale-105 active:scale-95"
            title="Reset Simulation"
          >
            <RotateCcw className="w-5 h-5" />
          </button>
        </div>
      </div>

      <div className="space-y-3">
        {/* LSM Tree Configuration */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('lsm')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">LSM Tree Configuration</h3>
            {expandedSections.lsm ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.lsm && (
            <div className="p-3 bg-dark-card">
              {/* Compaction Style Selector */}
              <div className="mb-3 pb-3 border-b border-dark-border">
                <label className="text-sm text-gray-300 flex items-center gap-1 mb-2">
                  Compaction Style
                  <div className="group relative">
                    <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                    <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                      Compaction strategy: Leveled (classic RocksDB) or Universal (space-efficient, lower write amplification)
                    </div>
                  </div>
                </label>
                <div className="flex gap-2">
                  <button
                    onClick={() => {
                      if (!isConnected || isRunning) return;
                      updateConfig({ compactionStyle: 'leveled' });
                    }}
                    disabled={!isConnected || isRunning}
                    className={`flex-1 px-3 py-2 text-sm border rounded transition-colors ${
                      compactionStyle === 'leveled'
                        ? 'bg-primary-500 border-primary-400 text-white font-semibold'
                        : 'bg-dark-bg hover:bg-gray-700 border-dark-border'
                    } disabled:opacity-50 disabled:cursor-not-allowed`}
                  >
                    Leveled
                  </button>
                  <button
                    onClick={() => {
                      if (!isConnected || isRunning) return;
                      updateConfig({ compactionStyle: 'universal' });
                    }}
                    disabled={!isConnected || isRunning}
                    className={`flex-1 px-3 py-2 text-sm border rounded transition-colors ${
                      compactionStyle === 'universal'
                        ? 'bg-primary-500 border-primary-400 text-white font-semibold'
                        : 'bg-dark-bg hover:bg-gray-700 border-dark-border'
                    } disabled:opacity-50 disabled:cursor-not-allowed`}
                  >
                    Universal
                  </button>
                  <button
                    onClick={() => {
                      if (!isConnected || isRunning) return;
                      updateConfig({ compactionStyle: 'fifo' });
                    }}
                    disabled={!isConnected || isRunning}
                    className={`flex-1 px-3 py-2 text-sm border rounded transition-colors ${
                      compactionStyle === 'fifo'
                        ? 'bg-primary-500 border-primary-400 text-white font-semibold'
                        : 'bg-dark-bg hover:bg-gray-700 border-dark-border'
                    } disabled:opacity-50 disabled:cursor-not-allowed`}
                  >
                    FIFO
                  </button>
                </div>
              </div>
              
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <ConfigInput label="Memtable Flush Size" field="memtableFlushSizeMB" min={1} max={2048} unit="MB"
                  tooltip="Size at which memtable is flushed to L0" />
                <ConfigInput label="Max Immutable Memtables" field="maxWriteBufferNumber" min={1} max={10}
                  tooltip="Max number of memtables before write stall" />
                <ConfigInput label="L0 Compaction Trigger" field="l0CompactionTrigger" min={2} max={20} unit="files"
                  tooltip="Number of L0 files that trigger compaction" />
                {compactionStyle !== 'fifo' && (
                  <>
                    <ConfigInput label="Level Size Multiplier" field="levelMultiplier" min={2} max={100}
                      tooltip="Size multiplier between levels (default: 10)" />
                    <ConfigInput label="Max Background Jobs" field="maxBackgroundJobs" min={1} max={32}
                      tooltip="RocksDB max_background_jobs: Max concurrent background threads for flushes AND compactions. Default: 2. Higher values allow more parallel operations but consume more CPU/memory." />
                    <ConfigInput label="Number of Levels" field="numLevels" min={2} max={10}
                      tooltip="Total number of LSM levels (including L0)" />
                  </>
                )}
                {compactionStyle === 'fifo' && (
                  <ConfigInput label="Max Background Jobs" field="maxBackgroundJobs" min={1} max={32}
                    tooltip="RocksDB max_background_jobs: Max concurrent background threads for flushes AND compactions. Default: 2. Higher values allow more parallel operations but consume more CPU/memory." />
                )}
              </div>

              {/* Advanced LSM Tuning (nested) */}
              <div className="border border-dark-border rounded mt-3 overflow-hidden">
                <button
                  onClick={() => toggleSection('lsmAdvanced')}
                  tabIndex={-1}
                  className="w-full flex items-center justify-between p-2 bg-dark-bg hover:bg-gray-700 transition-colors"
                >
                  <span className="text-xs font-medium flex items-center gap-1">
                    {expandedSections.lsmAdvanced ? '▼' : '▶'} Advanced Tuning
                  </span>
                </button>
                {expandedSections.lsmAdvanced && (
                  <div className="p-2 bg-dark-card">
                    <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                      {compactionStyle !== 'fifo' && (
                        <>
                          <ConfigInput label="Max Bytes for Level Base" field="maxBytesForLevelBaseMB" min={64} max={2048} unit="MB"
                            tooltip="Target size for L1 (RocksDB: max_bytes_for_level_base)" />
                          <ConfigInput label="Target SST File Size" field="targetFileSizeMB" min={1} max={512} unit="MB"
                            tooltip="Target size for individual SST files (RocksDB: target_file_size_base)" />
                          <ConfigInput label="File Size Multiplier" field="targetFileSizeMultiplier" min={1} max={10}
                            tooltip="SST file size multiplier per level (RocksDB: target_file_size_multiplier)" />
                        </>
                      )}
                      <ConfigInput label="Max Compaction Bytes" field="maxCompactionBytesMB" min={100} max={10000} unit="MB"
                        tooltip="Max total input size for single compaction (RocksDB: max_compaction_bytes)" />
                      {compactionStyle === 'universal' && (
                        <ConfigInput 
                          label="Max Size Amplification" 
                          field="maxSizeAmplificationPercent" 
                          min={0} 
                          max={10000} 
                          unit="%"
                          tooltip="Maximum allowed space amplification before compaction triggers (RocksDB: max_size_amplification_percent). Default: 200%. Higher values reduce compaction frequency but increase space usage. Value of 0 triggers compaction on any amplification, very high values (e.g., 9000) allow extreme amplification before triggering." />
                      )}
                      {compactionStyle === 'leveled' && (
                        <div className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            id="levelCompactionDynamicLevelBytes"
                            checked={levelCompactionDynamicLevelBytes}
                            onChange={(e) => {
                              if (!isConnected || isRunning) return;
                              updateConfig({ levelCompactionDynamicLevelBytes: e.target.checked });
                            }}
                            disabled={!isConnected || isRunning}
                            className="w-4 h-4 rounded border-gray-600 bg-dark-bg text-primary-500 focus:ring-primary-500 disabled:opacity-50 disabled:cursor-not-allowed"
                          />
                          <label htmlFor="levelCompactionDynamicLevelBytes" className="text-sm text-gray-300 flex items-center gap-1 cursor-pointer">
                            Dynamic Level Bytes
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Dynamically adjusts level sizes based on actual data distribution (RocksDB: level_compaction_dynamic_level_bytes). Default: true. When enabled, base_level may not be L1 - it's the first non-empty level. This allows RocksDB to adapt to sparse data distributions and avoid unnecessary intermediate levels.
                              </div>
                            </div>
                          </label>
                        </div>
                      )}
                      {compactionStyle === 'fifo' && (
                        <>
                          <ConfigInput
                            label="Max Table Files Size"
                            field="fifoMaxTableFilesSizeMB"
                            min={100}
                            max={10000000}
                            unit="MB"
                            tooltip="Total size threshold for FIFO deletion (RocksDB: max_table_files_size). Default: 1024 MB (1 GB). When total LSM size exceeds this, oldest files are deleted." />
                          <div className="flex items-center gap-2 col-span-1">
                            <input
                              type="checkbox"
                              id="fifoAllowCompaction"
                              checked={fifoAllowCompaction}
                              onChange={(e) => {
                                if (!isConnected || isRunning) return;
                                updateConfig({ fifoAllowCompaction: e.target.checked });
                              }}
                              disabled={!isConnected || isRunning}
                              className="w-4 h-4 rounded border-gray-600 bg-dark-bg text-primary-500 focus:ring-primary-500 disabled:opacity-50 disabled:cursor-not-allowed"
                            />
                            <label htmlFor="fifoAllowCompaction" className="text-sm text-gray-300 flex items-center gap-1 cursor-pointer">
                              Allow Compaction
                              <div className="group relative">
                                <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                                <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                  Enable intra-L0 compaction to merge small files (RocksDB: allow_compaction). Default: false. When enabled, FIFO can compact multiple small L0 files together to reduce file count while staying under size limits.
                                </div>
                              </div>
                            </label>
                          </div>
                        </>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* Workload & Traffic Pattern */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('workload')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">Write Traffic Model</h3>
            {expandedSections.workload ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.workload && (
            <div className="p-3 bg-dark-card">
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                {/* Write Rate - custom input to handle both writeRateMBps and trafficDistribution.baseRateMBps */}
                <div className="flex items-center justify-between gap-2">
                  <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                    Write Rate ({sustainableRangeStr ? `MB/s; max ${sustainableRangeStr}` : "MB/s"})
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        {`Incoming write throughput${burstMultiplier > 0 ? ' (base rate during quiet periods)' : ''} (0 = no writes)${sustainableRangeStr ? `\n\nSustainable rate range: ${sustainableRangeStr} MB/s` : ''}`}
                      </div>
                    </div>
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={1000}
                    step={0.1}
                    value={writeRate}
                    onChange={(e) => {
                      const val = parseFloat(e.target.value);
                      if (!isNaN(val)) {
                        const clamped = Math.max(0, Math.min(1000, val));
                        // Update both writeRateMBps and trafficDistribution.baseRateMBps if advanced model
                        if (trafficDist?.model === 'advanced') {
                          updateConfig({
                            writeRateMBps: clamped,
                            trafficDistribution: {
                              ...trafficDist,
                              baseRateMBps: clamped,
                            }
                          });
                        } else {
                          updateConfig({
                            writeRateMBps: clamped,
                            trafficDistribution: {
                              model: 'constant',
                              writeRateMBps: clamped,
                            }
                          });
                        }
                      }
                    }}
                    disabled={!isConnected}
                    className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  />
                </div>

                <div className="flex items-center justify-between gap-2">
                  <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                    Burstiness
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        Traffic burst multiplier. 0 = constant rate (no bursts). Values ≥ 2.0 enable advanced traffic model with ON/OFF periods and spikes. Example: 2.0 = traffic doubles during burst periods.
                      </div>
                    </div>
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={10.0}
                    step={0.1}
                    value={burstMultiplier}
                    onChange={(e) => {
                      const val = parseFloat(e.target.value);
                      if (!isNaN(val)) {
                        const clampedVal = Math.max(0, Math.min(10.0, val));
                        if (clampedVal === 0) {
                          updateConfig({
                            trafficDistribution: {
                              model: 'constant',
                              writeRateMBps: writeRate,
                            }
                          });
                        } else {
                          updateConfig({
                            trafficDistribution: {
                              ...(trafficDist || {}),
                              model: 'advanced',
                              baseRateMBps: writeRate,
                              burstMultiplier: clampedVal,
                              lognormalSigma: trafficDist?.lognormalSigma ?? 0.1,
                              onMeanSeconds: trafficDist?.onMeanSeconds ?? 5.0,
                              offMeanSeconds: trafficDist?.offMeanSeconds ?? 10.0,
                              erlangK: trafficDist?.erlangK ?? 2,
                              spikeRatePerSec: trafficDist?.spikeRatePerSec ?? 0.1,
                              spikeMeanDur: trafficDist?.spikeMeanDur ?? 1.0,
                              spikeAmplitudeMean: trafficDist?.spikeAmplitudeMean ?? 1.0,
                              spikeAmplitudeSigma: trafficDist?.spikeAmplitudeSigma ?? 0.5,
                              capacityLimitMB: trafficDist?.capacityLimitMB ?? 0,
                              queueMode: trafficDist?.queueMode ?? 'drop',
                            }
                          });
                        }
                      }
                    }}
                    disabled={!isConnected || isRunning}
                    className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  />
                </div>

                {/* Overlap Distribution - always visible */}
                <div className="flex items-center justify-between gap-2">
                  <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                    Overlap Distribution
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        Controls how many overlapping files are selected from the target level during compaction. This affects write amplification and compaction size. The overlap pattern depends on your workload: uniform writes create many overlaps, skewed workloads create fewer. Uniform: equal probability for any overlap count. Geometric: favors fewer overlaps (good for balanced workloads). Exponential: strongly favors fewer overlaps (good for skewed workloads).
                      </div>
                    </div>
                  </label>
                  <div className="flex items-center gap-2">
                    <select
                      value={overlapDistType}
                      onChange={(e) => {
                        try {
                          if (!isConnected || isRunning) return;
                          const newType = e.target.value as "uniform" | "exponential" | "geometric" | "fixed";
                          console.log('[OverlapDist] Changing type to:', newType, 'current overlapDist:', overlapDist);

                          // Ensure we have a valid overlapDist object with all required fields
                          const currentOverlapDist = overlapDist || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5, fixedPercentage: 0.5 };

                          // Create new overlap distribution config
                          const newOverlapDist: { type: "uniform" | "exponential" | "geometric" | "fixed"; geometricP?: number; exponentialLambda?: number; fixedPercentage?: number } = {
                            type: newType,
                            // Preserve existing parameters (they're optional, so only include if they exist)
                            ...(currentOverlapDist.geometricP !== undefined && { geometricP: currentOverlapDist.geometricP }),
                            ...(currentOverlapDist.exponentialLambda !== undefined && { exponentialLambda: currentOverlapDist.exponentialLambda }),
                            ...(currentOverlapDist.fixedPercentage !== undefined && { fixedPercentage: currentOverlapDist.fixedPercentage }),
                          };

                          // Ensure defaults are set for the selected type
                          if (newType === 'geometric' && newOverlapDist.geometricP === undefined) {
                            newOverlapDist.geometricP = 0.3;
                          }
                          if (newType === 'exponential' && newOverlapDist.exponentialLambda === undefined) {
                            newOverlapDist.exponentialLambda = 0.5;
                          }
                          if (newType === 'fixed' && newOverlapDist.fixedPercentage === undefined) {
                            newOverlapDist.fixedPercentage = 0.5;
                          }

                          console.log('[OverlapDist] New config:', newOverlapDist);

                          updateConfig({
                            overlapDistribution: newOverlapDist
                          });
                        } catch (error) {
                          console.error('[OverlapDist] Error changing distribution type:', error);
                          alert(`Error changing overlap distribution: ${error instanceof Error ? error.message : String(error)}`);
                        }
                      }}
                      disabled={!isConnected || isRunning}
                      className="w-32 px-3 py-1 bg-dark-bg border border-dark-border rounded text-gray-300 disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                    >
                      <option value="uniform">Uniform</option>
                      <option value="exponential">Exponential</option>
                      <option value="geometric">Geometric</option>
                      <option value="fixed">Fixed</option>
                    </select>
                    {overlapDistType === 'geometric' && (
                      <>
                        <label className="text-xs text-gray-400 flex items-center gap-1">
                          Bias:
                          <div className="group relative">
                            <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                            <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                              Geometric distribution success probability (P). Controls bias toward fewer overlaps. Higher values (0.5-0.9) = stronger bias toward 1 overlap. Lower values (0.1-0.3) = more balanced distribution. Default: 0.3 means 30% chance of 1 overlap, 21% chance of 2 overlaps, 14.7% chance of 3 overlaps, etc. (probability decreases geometrically as overlap count increases). Note: 0 overlaps (trivial moves) are handled separately and not part of this distribution.
                            </div>
                          </div>
                        </label>
                        <NumberInput
                          value={geometricP}
                          onChange={(val) => {
                            try {
                              const currentOverlapDist = overlapDist || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5 };
                              const clampedVal = Math.max(0.1, Math.min(0.9, val));
                              console.log('[OverlapDist] Updating geometricP to:', clampedVal);
                              updateConfig({
                                overlapDistribution: {
                                  ...currentOverlapDist,
                                  type: 'geometric',
                                  geometricP: clampedVal,
                                }
                              });
                            } catch (error) {
                              console.error('[OverlapDist] Error updating geometricP:', error);
                              alert(`Error updating bias: ${error instanceof Error ? error.message : String(error)}`);
                            }
                          }}
                          min={0.1}
                          max={0.9}
                          disabled={!isConnected || isRunning}
                          className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                        />
                      </>
                    )}
                    {overlapDistType === 'exponential' && (
                      <>
                        <label className="text-xs text-gray-400 flex items-center gap-1">
                          Bias:
                          <div className="group relative">
                            <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                            <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                              Exponential distribution rate parameter (Lambda). Controls bias toward fewer overlaps. Higher values (1.0-2.0) = stronger bias toward 1 overlap. Lower values (0.1-0.5) = more balanced distribution. Default: 0.5.
                            </div>
                          </div>
                        </label>
                        <NumberInput
                          value={exponentialLambda}
                          onChange={(val) => {
                            try {
                              const currentOverlapDist = overlapDist || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5 };
                              const clampedVal = Math.max(0.1, Math.min(2.0, val));
                              console.log('[OverlapDist] Updating exponentialLambda to:', clampedVal);
                              updateConfig({
                                overlapDistribution: {
                                  ...currentOverlapDist,
                                  type: 'exponential',
                                  exponentialLambda: clampedVal,
                                }
                              });
                            } catch (error) {
                              console.error('[OverlapDist] Error updating exponentialLambda:', error);
                              alert(`Error updating bias: ${error instanceof Error ? error.message : String(error)}`);
                            }
                          }}
                          min={0.1}
                          max={2.0}
                          disabled={!isConnected || isRunning}
                          className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                        />
                      </>
                    )}
                    {overlapDistType === 'fixed' && (
                      <>
                        <label className="text-xs text-gray-400 flex items-center gap-1">
                          Percentage:
                          <div className="group relative">
                            <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                            <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                              Fixed percentage of files from the target level that overlap. 0.0 = no overlaps (trivial moves only), 1.0 = all files overlap, 0.5 = 50% of files overlap. This provides deterministic overlap selection instead of probabilistic distributions.
                            </div>
                          </div>
                        </label>
                        <NumberInput
                          value={fixedPercentage}
                          onChange={(val) => {
                            try {
                              const currentOverlapDist = overlapDist || { type: 'fixed', fixedPercentage: 0.5 };
                              const clampedVal = Math.max(0.0, Math.min(1.0, val));
                              console.log('[OverlapDist] Updating fixedPercentage to:', clampedVal);
                              updateConfig({
                                overlapDistribution: {
                                  ...currentOverlapDist,
                                  type: 'fixed',
                                  fixedPercentage: clampedVal,
                                }
                              });
                            } catch (error) {
                              console.error('[OverlapDist] Error updating fixedPercentage:', error);
                              alert(`Error updating percentage: ${error instanceof Error ? error.message : String(error)}`);
                            }
                          }}
                          min={0.0}
                          max={1.0}
                          disabled={!isConnected || isRunning}
                          className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                        />
                      </>
                    )}
                  </div>
                </div>

                <ConfigInput label="Deduplication Factor" field="deduplicationFactor" min={0.1} max={1.0}
                  tooltip="Logical size after deduplication (0.9 = 10% from tombstones/overwrites, 1.0 = no dedup)" />
              </div>

              {/* Advanced Traffic Parameters (collapsible) - only show when burstiness > 0 */}
              {burstMultiplier > 0 && (
                <div className="mt-3 border border-dark-border rounded overflow-hidden">
                  <button
                    onClick={() => toggleSection('workloadAdvanced')}
                    tabIndex={-1}
                    className="w-full flex items-center justify-between p-2 bg-dark-bg hover:bg-gray-700 transition-colors"
                  >
                    <span className="text-xs font-medium flex items-center gap-1">
                      {expandedSections.workloadAdvanced ? '▼' : '▶'} Advanced Traffic Parameters
                    </span>
                  </button>
                  {expandedSections.workloadAdvanced && (
                    <div className="p-2 bg-dark-card">
                      <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Lognormal Sigma
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Controls how smooth or variable the traffic is. Lower values (0.01-0.1) = steady, predictable traffic. Higher values (0.5-2.0) = more random, unpredictable fluctuations.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={lognormalSigma}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  lognormalSigma: val,
                                }
                              });
                            }}
                            min={0.01}
                            max={2.0}
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            ON Period (sec)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Average duration of burst periods (ON state). Traffic rate is multiplied by burstiness during these periods.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={onMeanSeconds}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  onMeanSeconds: Math.max(0.1, Math.min(600.0, val)),
                                }
                              });
                            }}
                            min={0.1}
                            max={600.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            OFF Period (sec)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Average duration of quiet periods (OFF state). Traffic rate returns to base level.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={offMeanSeconds}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  offMeanSeconds: Math.max(0.1, Math.min(600.0, val)),
                                }
                              });
                            }}
                            min={0.1}
                            max={600.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Erlang K
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Controls regularity of ON/OFF periods. K=1 = exponential (highly variable). K=2+ = more regular, predictable intervals.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={erlangK}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  erlangK: Math.max(1, Math.min(10, Math.round(val))),
                                }
                              });
                            }}
                            min={1}
                            max={10}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Rate (per sec)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Average frequency of sudden traffic spikes (0 = no spikes). E.g., 0.1 = spike every 10 seconds on average.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={spikeRatePerSec}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  spikeRatePerSec: Math.max(0, Math.min(10.0, val)),
                                }
                              });
                            }}
                            min={0}
                            max={10.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Duration (sec)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Average duration of spikes. Spikes are short bursts of elevated traffic on top of base ON/OFF pattern.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={spikeMeanDur}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  spikeMeanDur: Math.max(0.1, Math.min(60.0, val)),
                                }
                              });
                            }}
                            min={0.1}
                            max={60.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Amplitude Mean
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean multiplier for spike magnitude. 1.0 = spikes double the current traffic, 2.0 = spikes triple it.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={spikeAmplitudeMean}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  spikeAmplitudeMean: Math.max(0.1, Math.min(10.0, val)),
                                }
                              });
                            }}
                            min={0.1}
                            max={10.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Amplitude Sigma
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Variability of spike amplitudes. 0.1 = consistent spike heights, 0.5+ = highly variable spikes.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={spikeAmplitudeSigma}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  spikeAmplitudeSigma: Math.max(0.01, Math.min(2.0, val)),
                                }
                              });
                            }}
                            min={0.01}
                            max={2.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Capacity Limit (MB)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Maximum buffer capacity for queued writes (0 = unlimited). When full, behavior depends on Queue Mode setting.
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={capacityLimitMB}
                            onChange={(val) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...(trafficDist || { model: 'advanced' }),
                                  model: 'advanced',
                                  capacityLimitMB: Math.max(0, Math.min(10000, val)),
                                }
                              });
                            }}
                            min={0}
                            max={10000}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Queue Mode
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                When capacity is full: Drop = discard excess writes, Queue = buffer them (may cause OOM).
                              </div>
                            </div>
                          </label>
                          <select
                            value={queueMode}
                            onChange={(e) => {
                              updateConfig({
                                trafficDistribution: {
                                  ...trafficDist,
                                  model: 'advanced',
                                  queueMode: e.target.value as 'drop' | 'queue',
                                }
                              });
                            }}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-xs text-gray-300 disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          >
                            <option value="drop">Drop</option>
                            <option value="queue">Queue</option>
                          </select>
                        </div>

                      </div>
                    </div>
                  )}
                </div>
              )}

              {sustainableRangeStr && (
                <details className="mt-2 text-xs text-gray-400">
                  <summary className="cursor-pointer hover:text-gray-300 font-medium">Worst-case scenario explanation</summary>
                  <div className="mt-2 p-3 bg-gray-900 rounded border border-gray-700 space-y-2 font-mono text-xs max-h-96 overflow-y-auto">
                    <div className="font-semibold text-yellow-400 mb-2">How Bad Can Leveled Compaction Get?</div>
                    <div>
                      <div className="text-yellow-300 mb-1">Worst-Case Scenario:</div>
                      <div className="pl-2 space-y-1 text-gray-300">
                        <div>• {maxBackgroundJobs} parallel compactions scheduled between deepest levels</div>
                        <div>• Each compaction pattern: Read 2 source files + 1 target file (overlap) + Write 1 output file</div>
                        <div>• File sizes scale exponentially with level depth (up to 1.6GB-2GB per file)</div>
                        <div>• With serialized execution (diskBusyUntil), compactions run sequentially</div>
                        <div>• During compaction burst, flushes are blocked (disk fully consumed)</div>
                        <div>• Writes continue arriving and accumulate in memtable buffer</div>
                      </div>
                    </div>
                    <div>
                      <div className="text-yellow-300 mb-1">Example Calculation (L5→L6, {maxBackgroundJobs} compactions):</div>
                      <div className="pl-2 space-y-1 text-gray-300">
                        <div>• Per compaction I/O: 4 files × 1600 MB = 6400 MB</div>
                        <div>• Total queued I/O: {maxBackgroundJobs} × 6400 MB = {(maxBackgroundJobs * 6400 / 1024).toFixed(1)} GB</div>
                        <div>• Duration: {(maxBackgroundJobs * 6400 / ioThroughput).toFixed(0)}s at {ioThroughput} MB/s disk</div>
                        <div>• Buffer capacity: {bufferCapacityMB} MB ({bufferCapacityMB / 1024} GB)</div>
                        <div>• Minimum sustainable: {bufferCapacityMB} MB ÷ {(maxBackgroundJobs * 6400 / ioThroughput).toFixed(0)}s = {minSustainableRate?.toFixed(1)} MB/s</div>
                      </div>
                    </div>
                    <div>
                      <div className="text-yellow-300 mb-1">Why maxBackgroundJobs Matters:</div>
                      <div className="pl-2 space-y-1 text-gray-300">
                        <div>• maxBackgroundJobs determines how many compactions can queue up</div>
                        <div>• More parallel compactions = longer total duration = more writes accumulate</div>
                        <div>• Sustainable rate scales inversely with maxBackgroundJobs</div>
                        <div>• Example: {maxBackgroundJobs} jobs → {minSustainableRate?.toFixed(1)} MB/s, 1 job → {(bufferCapacityMB / (6400 / ioThroughput)).toFixed(1)} MB/s</div>
                      </div>
                    </div>
                    <div>
                      <div className="text-yellow-300 mb-1">Real-World Implications:</div>
                      <div className="pl-2 space-y-1 text-gray-300">
                        <div>• If LSM gets into bad shape, only way to fix is often to stop writes</div>
                        <div>• RocksDB doesn't prevent worst-case scheduling - it relies on write throttling</div>
                        <div>• This simulation shows what CAN happen, not what SHOULD happen</div>
                        <div>• Monitoring and proactive tuning are essential for production systems</div>
                      </div>
                    </div>
                    <div className="text-gray-500 italic mt-2 pt-2 border-t border-gray-700">
                      Note: This is a simplified worst-case estimate. Actual LSM behavior depends on many factors including compaction patterns, file sizes, workload characteristics, and RocksDB's compaction scheduling heuristics.
                    </div>
                  </div>
                </details>
              )}
              {isExceedingSustainable && (
                <div className="mt-2 text-xs text-red-400 flex items-center gap-1">
                  <AlertTriangle className="w-3 h-3" />
                  <span>Write rate exceeds sustainable limit ({sustainableRangeStr || maxSustainableRate?.toFixed(1)} MB/s)</span>
                </div>
              )}
            </div>
          )}
        </div>

        {/* Compression Configuration */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('compression')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">Compression</h3>
            {expandedSections.compression ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.compression && (
            <div className="p-3 bg-dark-card">
              <div className="text-xs text-gray-400 mb-3">
                Compression affects both read and write paths: reduces physical storage size and I/O, but adds CPU overhead. Compression/decompression time is modeled additively with disk I/O (compress → write → seek). Slower speeds increase operation duration. Choose a preset algorithm or customize for your workload.
              </div>
              <div className="flex items-center gap-3 mb-3">
                <label className="text-sm text-gray-300">Compression Presets</label>
                <select
                  value={getCompressionPreset()}
                  onChange={(e) => {
                    if (!isConnected || isRunning) return;
                    const preset = e.target.value;
                    if (preset === 'lz4') {
                      updateConfig({
                        compressionFactor: 0.85,
                        compressionThroughputMBps: 750,
                        decompressionThroughputMBps: 3700,
                        sstableBuildThroughputMBps: 75,
                        blockSizeKB: 4,
                      });
                    } else if (preset === 'snappy') {
                      updateConfig({
                        compressionFactor: 0.83,
                        compressionThroughputMBps: 530,
                        decompressionThroughputMBps: 1800,
                        sstableBuildThroughputMBps: 90,
                        blockSizeKB: 4,
                      });
                    } else if (preset === 'zstd') {
                      updateConfig({
                        compressionFactor: 0.70,
                        compressionThroughputMBps: 470,
                        decompressionThroughputMBps: 1380,
                        sstableBuildThroughputMBps: 50,
                        blockSizeKB: 4,
                      });
                    } else if (preset === 'none') {
                      updateConfig({
                        compressionFactor: 1.0,
                        compressionThroughputMBps: 0,
                        decompressionThroughputMBps: 0,
                        sstableBuildThroughputMBps: 200,
                        blockSizeKB: 4,
                      });
                    }
                  }}
                  disabled={!isConnected || isRunning}
                  className="px-3 py-1 bg-dark-bg border border-dark-border rounded text-sm disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                >
                  <option value="lz4">LZ4 (Fast, 15% compression)</option>
                  <option value="snappy">Snappy (Balanced, 17% compression)</option>
                  <option value="zstd">Zstd (Best compression, 30%)</option>
                  <option value="none">None (No compression)</option>
                  <option value="custom">Custom</option>
                </select>
              </div>
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <ConfigInput
                  label="Compression Factor"
                  field="compressionFactor"
                  min={0.1}
                  max={1.0}
                  tooltip="Physical size after compression (0.85 = 15% reduction with LZ4, 0.70 = 30% with Zstd, 1.0 = no compression)" />
                <ConfigInput
                  label="Block Size"
                  field="blockSizeKB"
                  min={1}
                  max={64}
                  unit="KB"
                  tooltip="SST block size (RocksDB default: 4 KB). Larger blocks improve compression ratio but increase bytes read/decompressed per key lookup. Smaller blocks reduce per-read overhead but compress less efficiently." />
                <ConfigInput
                  label="Compression Speed"
                  field="compressionThroughputMBps"
                  min={0}
                  max={5000}
                  unit="MB/s"
                  tooltip="LEGACY: Kept for compatibility. For write path (flush/compaction), use 'SSTable Build Rate' instead. This parameter is unused in current implementation." />
                <ConfigInput
                  label="Decompression Speed"
                  field="decompressionThroughputMBps"
                  min={0}
                  max={10000}
                  unit="MB/s"
                  tooltip="CPU throughput for decompression (0 = infinite/no CPU cost). LZ4: 3700 MB/s, Snappy: 1800 MB/s, Zstd: 1380 MB/s (single-threaded)" />
              </div>
            </div>
          )}
        </div>

        {/* Read Traffic Model */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('readWorkload')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">Read Traffic Model</h3>
            {expandedSections.readWorkload ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.readWorkload && (
            <div className="p-3 bg-dark-card">
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <div className="flex items-center justify-between gap-2">
                  <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                    Requests/sec
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        Total read requests per second (0 = disabled). The simulator calculates latency percentiles and disk bandwidth based on this rate, cache hit rate, and LSM structure.
                      </div>
                    </div>
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={1000000}
                    step={100}
                    value={readWorkload?.requestsPerSec || 0}
                    onChange={(e) => {
                      const val = parseFloat(e.target.value);
                      if (!isNaN(val)) {
                        const reqsPerSec = Math.max(0, val);
                        if (reqsPerSec === 0) {
                          updateConfig({ readWorkload: undefined });
                        } else {
                          updateConfig({
                            readWorkload: {
                              enabled: true,
                              requestsPerSec: reqsPerSec,
                              requestRateVariability: readWorkload?.requestRateVariability ?? 0.0,
                              cacheHitRate: readWorkload?.cacheHitRate ?? 0.90,
                              bloomNegativeRate: readWorkload?.bloomNegativeRate ?? 0.02,
                              scanRate: readWorkload?.scanRate ?? 0.05,
                              cacheHitLatency: readWorkload?.cacheHitLatency ?? { distribution: 'fixed', mean: 0.001 },
                              bloomNegativeLatency: readWorkload?.bloomNegativeLatency ?? { distribution: 'fixed', mean: 0.01 },
                              pointLookupLatency: readWorkload?.pointLookupLatency ?? { distribution: 'exponential', mean: 2.0 },
                              scanLatency: readWorkload?.scanLatency ?? { distribution: 'lognormal', mean: 10.0 },
                              avgScanSizeKB: readWorkload?.avgScanSizeKB ?? 16.0,
                            }
                          });
                        }
                      }
                    }}
                    disabled={!isConnected || isRunning}
                    className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  />
                </div>
              </div>

              {/* Request Rate Variability - only show when requestsPerSec > 0 */}
              {(readWorkload?.requestsPerSec || 0) > 0 && (
                <div className="flex items-center justify-between gap-2">
                  <label className="text-xs text-gray-400 flex items-center gap-1">
                    Request Rate Variability
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        Controls random fluctuation in request rate. 0 = constant rate, 0.2 = ±20% variation (typical), 0.5 = ±50% variation (high volatility). Uses coefficient of variation model - simpler than write traffic burstiness.
                      </div>
                    </div>
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={0.5}
                    step={0.01}
                    value={readWorkload?.requestRateVariability ?? 0}
                    onChange={(e) => {
                      if (!isConnected || isRunning) return;
                      const variability = parseFloat(e.target.value);
                      if (isNaN(variability) || variability < 0 || variability > 0.5) return;
                      updateConfig({
                        readWorkload: {
                          enabled: true,
                          requestsPerSec: readWorkload?.requestsPerSec ?? 1000,
                          requestRateVariability: variability,
                          cacheHitRate: readWorkload?.cacheHitRate ?? 0.90,
                          bloomNegativeRate: readWorkload?.bloomNegativeRate ?? 0.02,
                          scanRate: readWorkload?.scanRate ?? 0.05,
                          cacheHitLatency: readWorkload?.cacheHitLatency ?? { distribution: 'fixed', mean: 0.001 },
                          bloomNegativeLatency: readWorkload?.bloomNegativeLatency ?? { distribution: 'fixed', mean: 0.01 },
                          pointLookupLatency: readWorkload?.pointLookupLatency ?? { distribution: 'exponential', mean: 2.0 },
                          scanLatency: readWorkload?.scanLatency ?? { distribution: 'lognormal', mean: 10.0 },
                          avgScanSizeKB: readWorkload?.avgScanSizeKB ?? 16.0,
                        }
                      });
                    }}
                    disabled={!isConnected || isRunning}
                    className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  />
                </div>
              )}

              {/* Read Traffic Presets */}
              {(readWorkload?.requestsPerSec || 0) > 0 && (
                <div className="flex items-center gap-3 mb-2">
                  <label className="text-sm text-gray-300">Read Traffic Presets</label>
                  <select
                    value="custom"
                    onChange={(e) => {
                      if (!isConnected || isRunning) return;
                      const preset = e.target.value;
                      const reqsPerSec = readWorkload?.requestsPerSec || 1000;

                      if (preset === 'light') {
                        updateConfig({
                          readWorkload: {
                            enabled: true,
                            requestsPerSec: reqsPerSec,
                            requestRateVariability: 0.0,
                            cacheHitRate: 0.95,
                            bloomNegativeRate: 0.01,
                            scanRate: 0.02,
                            cacheHitLatency: { distribution: 'fixed', mean: 0.001 },
                            bloomNegativeLatency: { distribution: 'fixed', mean: 0.01 },
                            pointLookupLatency: { distribution: 'exponential', mean: 1.5 },
                            scanLatency: { distribution: 'lognormal', mean: 8.0 },
                            avgScanSizeKB: 8.0,
                          }
                        });
                      } else if (preset === 'moderate') {
                        updateConfig({
                          readWorkload: {
                            enabled: true,
                            requestsPerSec: reqsPerSec,
                            requestRateVariability: 0.0,
                            cacheHitRate: 0.90,
                            bloomNegativeRate: 0.02,
                            scanRate: 0.05,
                            cacheHitLatency: { distribution: 'fixed', mean: 0.001 },
                            bloomNegativeLatency: { distribution: 'fixed', mean: 0.01 },
                            pointLookupLatency: { distribution: 'exponential', mean: 2.0 },
                            scanLatency: { distribution: 'lognormal', mean: 10.0 },
                            avgScanSizeKB: 16.0,
                          }
                        });
                      } else if (preset === 'heavy') {
                        updateConfig({
                          readWorkload: {
                            enabled: true,
                            requestsPerSec: reqsPerSec,
                            requestRateVariability: 0.0,
                            cacheHitRate: 0.80,
                            bloomNegativeRate: 0.03,
                            scanRate: 0.10,
                            cacheHitLatency: { distribution: 'fixed', mean: 0.001 },
                            bloomNegativeLatency: { distribution: 'fixed', mean: 0.01 },
                            pointLookupLatency: { distribution: 'exponential', mean: 3.0 },
                            scanLatency: { distribution: 'lognormal', mean: 15.0 },
                            avgScanSizeKB: 32.0,
                          }
                        });
                      }
                    }}
                    disabled={!isConnected || isRunning}
                    className="px-3 py-1 bg-dark-bg border border-dark-border rounded text-sm disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  >
                    <option value="light">Light (95% cache, fast)</option>
                    <option value="moderate">Moderate (90% cache, balanced)</option>
                    <option value="heavy">Heavy (80% cache, more scans)</option>
                    <option value="custom">Custom</option>
                  </select>
                </div>
              )}

              {/* Advanced Read Parameters (collapsible) - only show when requestsPerSec > 0 */}
              {(readWorkload?.requestsPerSec || 0) > 0 && (
                <div className="mt-3 border border-dark-border rounded overflow-hidden">
                  <button
                    onClick={() => toggleSection('readWorkloadAdvanced')}
                    tabIndex={-1}
                    className="w-full flex items-center justify-between p-2 bg-dark-bg hover:bg-gray-700 transition-colors"
                  >
                    <span className="text-xs font-medium flex items-center gap-1">
                      {expandedSections.readWorkloadAdvanced ? '▼' : '▶'} Advanced Read Parameters
                    </span>
                  </button>
                  {expandedSections.readWorkloadAdvanced && (
                    <div className="p-2 bg-dark-card">
                      <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                        {/* Cache Hit Rate */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Cache Hit Rate
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Percentage of reads served from block cache (0.0-1.0). Default: 0.90 (90%)
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.cacheHitRate ?? 0.90}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({ cacheHitRate: Math.max(0, Math.min(1.0, val)) })
                              });
                            }}
                            min={0}
                            max={1.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Bloom Negative Rate */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Bloom Negative Rate
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Percentage of reads that are bloom filter negatives (0.0-1.0). Default: 0.02 (2%)
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.bloomNegativeRate ?? 0.02}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({ bloomNegativeRate: Math.max(0, Math.min(1.0, val)) })
                              });
                            }}
                            min={0}
                            max={1.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Scan Rate */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Scan Rate
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Percentage of reads that are range scans (0.0-1.0). Remaining % = point lookups. Default: 0.05 (5%)
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.scanRate ?? 0.05}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({ scanRate: Math.max(0, Math.min(1.0, val)) })
                              });
                            }}
                            min={0}
                            max={1.0}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Point Lookup Rate (calculated, read-only) */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Point Lookup Rate
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Calculated as: 1.0 - CacheHitRate - BloomNegativeRate - ScanRate. These are point lookups that miss cache and must read from disk.
                              </div>
                            </div>
                          </label>
                          <div className="w-20 px-2 py-1 bg-gray-800 border border-dark-border rounded text-right text-xs text-gray-400">
                            {(() => {
                              const cacheHitRate = readWorkload?.cacheHitRate ?? 0.90;
                              const bloomNegativeRate = readWorkload?.bloomNegativeRate ?? 0.02;
                              const scanRate = readWorkload?.scanRate ?? 0.05;
                              const pointLookupRate = Math.max(0, 1.0 - cacheHitRate - bloomNegativeRate - scanRate);
                              return pointLookupRate.toFixed(4);
                            })()}
                          </div>
                        </div>

                        {/* Avg Scan Size */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Avg Scan Size (KB)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Average size of range scans in KB. Default: 16 KB
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.avgScanSizeKB ?? 16.0}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({ avgScanSizeKB: Math.max(1, val) })
                              });
                            }}
                            min={1}
                            max={10000}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        <div className="col-span-2 mt-2 pt-2 border-t border-dark-border">
                          <h5 className="text-xs font-semibold text-gray-400 uppercase mb-2">Latency Configuration</h5>
                        </div>

                        {/* Cache Hit Latency */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Cache Hit Latency (ms)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean latency for cache hits (fixed distribution). Default: 0.001 ms (1 μs)
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.cacheHitLatency?.mean ?? 0.001}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({
                                  cacheHitLatency: { distribution: 'fixed', mean: Math.max(0.0001, val) }
                                })
                              });
                            }}
                            min={0.0001}
                            max={100}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Bloom Negative Latency */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Bloom Negative Latency (ms)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean latency for bloom filter negatives (fixed distribution). Default: 0.01 ms (10 μs)
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.bloomNegativeLatency?.mean ?? 0.01}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({
                                  bloomNegativeLatency: { distribution: 'fixed', mean: Math.max(0.0001, val) }
                                })
                              });
                            }}
                            min={0.0001}
                            max={100}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Point Lookup Latency */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Point Lookup Latency (ms)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean latency for point lookups with cache miss (exponential distribution, scaled by read amplification). Default: 2.0 ms
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.pointLookupLatency?.mean ?? 2.0}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({
                                  pointLookupLatency: { distribution: 'exponential', mean: Math.max(0.1, val) }
                                })
                              });
                            }}
                            min={0.1}
                            max={1000}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                        {/* Scan Latency */}
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Scan Latency (ms)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean latency for range scans (lognormal distribution). Default: 10.0 ms
                              </div>
                            </div>
                          </label>
                          <NumberInput
                            value={readWorkload?.scanLatency?.mean ?? 10.0}
                            onChange={(val) => {
                              updateConfig({
                                readWorkload: getCompleteReadWorkload({
                                  scanLatency: { distribution: 'lognormal', mean: Math.max(0.1, val) }
                                })
                              });
                            }}
                            min={0.1}
                            max={10000}
                            disabled={!isConnected || isRunning}
                            className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>

                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>

        {/* I/O Performance */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('io')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">I/O Performance</h3>
            {expandedSections.io ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.io && (
            <div className="p-3 bg-dark-card space-y-2">
              <div className="flex gap-2">
                <PresetButton
                  label="NVMe"
                  onClick={() => { handleConfigChange('ioLatencyMs', 0.1); handleConfigChange('ioThroughputMBps', 3500); }}
                  disabled={!isConnected || isRunning}
                  isSelected={Math.abs(ioLatency - 0.1) < 0.01 && Math.abs(ioThroughput - 3500) < 1}
                />
                <PresetButton
                  label="SATA"
                  onClick={() => { handleConfigChange('ioLatencyMs', 0.2); handleConfigChange('ioThroughputMBps', 500); }}
                  disabled={!isConnected || isRunning}
                  isSelected={Math.abs(ioLatency - 0.2) < 0.01 && Math.abs(ioThroughput - 500) < 1}
                />
                <PresetButton
                  label="EBS gp3"
                  onClick={() => { handleConfigChange('ioLatencyMs', 1); handleConfigChange('ioThroughputMBps', 125); }}
                  disabled={!isConnected || isRunning}
                  isSelected={Math.abs(ioLatency - 1) < 0.1 && Math.abs(ioThroughput - 125) < 1}
                />
                <PresetButton
                  label="HDD"
                  onClick={() => { handleConfigChange('ioLatencyMs', 10); handleConfigChange('ioThroughputMBps', 160); }}
                  disabled={!isConnected || isRunning}
                  isSelected={Math.abs(ioLatency - 10) < 0.1 && Math.abs(ioThroughput - 160) < 1}
                />
              </div>
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <ConfigInput label="I/O Latency" field="ioLatencyMs" min={0.1} max={50} unit="ms"
                  tooltip="Disk operation latency" />
                <ConfigInput label="I/O Throughput" field="ioThroughputMBps" min={10} max={10000} unit="MB/s"
                  tooltip="Max disk bandwidth (shared by all operations)" />
                <ConfigInput label="SSTable Build Rate" field="sstableBuildThroughputMBps" min={0} max={1000} unit="MB/s"
                  tooltip="CPU throughput for building SSTables (compression + bloom filters + index). Includes all CPU work during flush/compaction. Set to 0 for infinite (no CPU cost). LZ4: ~75 MB/s, Snappy: ~75-100 MB/s, Zstd: ~50 MB/s, No compression: ~200 MB/s" />
              </div>

              {/* WAL Configuration */}
              <div className="mt-4 pt-4 border-t border-dark-border">
                <h4 className="text-xs font-semibold text-gray-400 uppercase mb-3">Write-Ahead Log (WAL)</h4>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      id="enableWAL"
                      checked={enableWAL}
                      onChange={(e) => {
                        if (!isConnected || isRunning) return;
                        updateConfig({ enableWAL: e.target.checked });
                      }}
                      disabled={!isConnected || isRunning}
                      className="w-4 h-4 rounded border-gray-600 bg-dark-bg text-primary-500 focus:ring-primary-500 disabled:opacity-50 disabled:cursor-not-allowed"
                    />
                    <label htmlFor="enableWAL" className="text-sm text-gray-300 flex items-center gap-1 cursor-pointer">
                      Enable WAL
                      <div className="group relative">
                        <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                        <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                          Enable Write-Ahead Log for durability (RocksDB default: true). WAL writes occur before memtable inserts and affect disk I/O contention, but are NOT included in write amplification calculations.
                        </div>
                      </div>
                    </label>
                  </div>

                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      id="walSync"
                      checked={walSync}
                      onChange={(e) => {
                        if (!isConnected || isRunning || !enableWAL) return;
                        updateConfig({ walSync: e.target.checked });
                      }}
                      disabled={!enableWAL || !isConnected || isRunning}
                      className="w-4 h-4 rounded border-gray-600 bg-dark-bg text-primary-500 focus:ring-primary-500 disabled:opacity-50 disabled:cursor-not-allowed"
                    />
                    <label htmlFor="walSync" className="text-sm text-gray-300 flex items-center gap-1 cursor-pointer">
                      WAL Sync
                      <div className="group relative">
                        <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                        <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                          Sync WAL after each write with fsync() (RocksDB default: false). When enabled, adds sync latency but guarantees durability on machine crashes.
                        </div>
                      </div>
                    </label>
                  </div>

                  {walSync && (
                    <div className="ml-6">
                      <ConfigInput
                        label="WAL Sync Latency"
                        field="walSyncLatencyMs"
                        min={0.1}
                        max={50}
                        unit="ms"
                        disabled={!enableWAL}
                        tooltip="fsync() latency in milliseconds (default: 1.5ms for NVMe/SSD)" />
                    </div>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>

        {/* Simulation Configuration */}
        <div className="border border-dark-border rounded-lg overflow-hidden">
          <button
            onClick={() => toggleSection('simulation')}
            tabIndex={-1}
            className="w-full flex items-center justify-between p-3 bg-dark-bg hover:bg-gray-800 transition-colors"
          >
            <h3 className="font-semibold text-sm">Simulation Configuration</h3>
            {expandedSections.simulation ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.simulation && (
            <div className="p-3 bg-dark-card">
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <ConfigInput label="Simulation Speed" field="simulationSpeedMultiplier" min={1} max={100} unit="x"
                  tooltip="Speed multiplier for fast-forward simulation" />
                <ConfigInput label="Initial LSM Size" field="initialLSMSizeMB" min={0} max={100000} unit="MB"
                  tooltip="⚠️ Pre-populate LSM tree (requires reset)" />
                <ConfigInput label="Random Seed" field="randomSeed" min={0} max={999999}
                  tooltip="Random seed for reproducibility (0 = random)" />
                <ConfigInput label="Max Stalled Write Memory" field="maxStalledWriteMemoryMB" min={0} max={100000} unit="MB"
                  tooltip="OOM threshold: stop simulation if stalled write backlog exceeds this (0 = unlimited, default: 4096 MB)" />
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
