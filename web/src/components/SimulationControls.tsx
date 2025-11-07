import { useState, useEffect } from 'react';
import { Play, Pause, RotateCcw, Settings, ChevronDown, ChevronRight, AlertTriangle, HelpCircle, RefreshCw } from 'lucide-react';
import { useStore } from '../store';
import type { SimulationConfig } from '../types';
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
  const trafficModel = useStore(state => state.config.trafficDistribution?.model) || 'constant';
  const overlapDistTypeRaw = useStore(state => state.config.overlapDistribution?.type);
  const overlapDistType = (overlapDistTypeRaw === 'uniform' || overlapDistTypeRaw === 'exponential' || overlapDistTypeRaw === 'geometric') 
    ? overlapDistTypeRaw 
    : 'geometric'; // Default to geometric if invalid or missing
  
  // Extract all traffic distribution values (must be at top level for hooks)
  const trafficDist = useStore(state => state.config.trafficDistribution);
  const baseRateMBps = trafficDist?.baseRateMBps || 10.0;
  const burstMultiplier = trafficDist?.burstMultiplier || 2.0;
  const lognormalSigma = trafficDist?.lognormalSigma || 0.1;
  const onMeanSeconds = trafficDist?.onMeanSeconds || 5.0;
  const offMeanSeconds = trafficDist?.offMeanSeconds || 10.0;
  const erlangK = trafficDist?.erlangK || 2;
  const spikeRatePerSec = trafficDist?.spikeRatePerSec || 0.1;
  const spikeMeanDur = trafficDist?.spikeMeanDur || 1.0;
  const spikeAmplitudeMean = trafficDist?.spikeAmplitudeMean || 1.0;
  const spikeAmplitudeSigma = trafficDist?.spikeAmplitudeSigma || 0.5;
  const capacityLimitMB = trafficDist?.capacityLimitMB || 0;
  const queueMode = trafficDist?.queueMode || 'drop';
  
  // Extract all overlap distribution values
  const overlapDist = useStore(state => state.config.overlapDistribution);
  const geometricP = overlapDist?.geometricP || 0.3;
  const exponentialLambda = overlapDist?.exponentialLambda || 0.5;
  
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
  const [expandedSections, setExpandedSections] = useState({
    lsm: true,
    lsmAdvanced: false,
    workload: true,
    workloadAdvanced: false,
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
                </div>
              </div>
              
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <ConfigInput label="Memtable Flush Size" field="memtableFlushSizeMB" min={1} max={512} unit="MB"
                  tooltip="Size at which memtable is flushed to L0" />
                <ConfigInput label="Max Immutable Memtables" field="maxWriteBufferNumber" min={1} max={10}
                  tooltip="Max number of memtables before write stall" />
                <ConfigInput label="L0 Compaction Trigger" field="l0CompactionTrigger" min={2} max={20} unit="files"
                  tooltip="Number of L0 files that trigger compaction" />
                <ConfigInput label="Level Size Multiplier" field="levelMultiplier" min={2} max={100}
                  tooltip="Size multiplier between levels (default: 10)" />
                <ConfigInput label="Compaction Parallelism" field="maxBackgroundJobs" min={1} max={32}
                  tooltip="Max concurrent compaction jobs" />
                <ConfigInput label="Number of Levels" field="numLevels" min={2} max={10}
                  tooltip="Total number of LSM levels (including L0)" />
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
                      <ConfigInput label="Max Bytes for Level Base" field="maxBytesForLevelBaseMB" min={64} max={2048} unit="MB"
                        tooltip="Target size for L1 (RocksDB: max_bytes_for_level_base)" />
                      <ConfigInput label="Target SST File Size" field="targetFileSizeMB" min={1} max={512} unit="MB"
                        tooltip="Target size for individual SST files (RocksDB: target_file_size_base)" />
                      <ConfigInput label="File Size Multiplier" field="targetFileSizeMultiplier" min={1} max={10}
                        tooltip="SST file size multiplier per level (RocksDB: target_file_size_multiplier)" />
                      <ConfigInput label="Max Compaction Bytes" field="maxCompactionBytesMB" min={100} max={10000} unit="MB"
                        tooltip="Max total input size for single compaction (RocksDB: max_compaction_bytes)" />
                      <ConfigInput label="Max Subcompactions" field="maxSubcompactions" min={1} max={16}
                        tooltip="Parallelism within a single compaction job (RocksDB: max_subcompactions). Default: 1 (disabled). Splits large compactions into multiple parallel subcompactions, reducing compaction duration. Applies to L0→L1 compactions for leveled style, and L0→L1+ compactions for universal style. Higher values (e.g., 4-8) can significantly speed up large L0 compactions." />
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
            <h3 className="font-semibold text-sm">Workload & Traffic Pattern</h3>
            {expandedSections.workload ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
          {expandedSections.workload && (
            <div className="p-3 bg-dark-card">
              {/* Traffic Model Selector */}
              <div className="mb-3 pb-3 border-b border-dark-border">
                <label className="text-sm text-gray-300 flex items-center gap-1 mb-2">
                  Traffic Model
                  <div className="group relative">
                    <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                    <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                      Choose traffic distribution model: Constant Rate (simple) or Advanced (ON/OFF with spikes)
                    </div>
                  </div>
                </label>
                <div className="flex gap-2">
                  <button
                    onClick={() => {
                      if (!isConnected || isRunning) return;
                      updateConfig({ 
                        trafficDistribution: {
                          model: 'constant',
                          writeRateMBps: writeRate,
                        }
                      });
                    }}
                    disabled={!isConnected || isRunning}
                    className={`flex-1 px-3 py-2 text-sm border rounded transition-colors ${
                      trafficModel === 'constant'
                        ? 'bg-primary-500 border-primary-400 text-white font-semibold'
                        : 'bg-dark-bg hover:bg-gray-700 border-dark-border'
                    } disabled:opacity-50 disabled:cursor-not-allowed`}
                  >
                    Constant Rate
                  </button>
                  <button
                    onClick={() => {
                      if (!isConnected || isRunning) return;
                      updateConfig({ 
                        trafficDistribution: {
                          model: 'advanced',
                          baseRateMBps: writeRate,
                          burstMultiplier: 2.0,
                          lognormalSigma: 0.1,
                          onMeanSeconds: 5.0,
                          offMeanSeconds: 10.0,
                          erlangK: 2,
                          spikeRatePerSec: 0.1,
                          spikeMeanDur: 1.0,
                          spikeAmplitudeMean: 1.0,
                          spikeAmplitudeSigma: 0.5,
                          capacityLimitMB: 0,
                          queueMode: 'drop',
                        }
                      });
                    }}
                    disabled={!isConnected || isRunning}
                    className={`flex-1 px-3 py-2 text-sm border rounded transition-colors ${
                      trafficModel === 'advanced'
                        ? 'bg-primary-500 border-primary-400 text-white font-semibold'
                        : 'bg-dark-bg hover:bg-gray-700 border-dark-border'
                    } disabled:opacity-50 disabled:cursor-not-allowed`}
                  >
                    Advanced (ON/OFF + Spikes)
                  </button>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                {trafficModel === 'constant' ? (
                  <ConfigInput 
                    label="Write Rate" 
                    field="writeRateMBps" 
                    min={0} 
                    max={1000} 
                    unit={sustainableRangeStr ? `MB/s; max ${sustainableRangeStr}` : "MB/s"}
                    tooltip={`Incoming write throughput (0 = no writes)${sustainableRangeStr ? `\n\nSustainable rate range: ${sustainableRangeStr} MB/s\n\nThis range accounts for:\n• Conservative estimate (upper bound): Average compaction overhead\n• Worst-case estimate (lower bound): Buffer capacity during worst-case compaction bursts\n\nSee detailed explanation below for worst-case scenarios.` : ''}`} />
                ) : (
                  <>
                    <div className="flex items-center justify-between gap-2">
                      <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                        Base Rate (MB/s)
                        <div className="group relative">
                          <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                          <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                            Average write rate during normal operation (quiet periods). This is the baseline traffic level before bursts or spikes.
                          </div>
                        </div>
                      </label>
                      <input
                        type="number"
                        min={0}
                        max={1000}
                        
                        value={baseRateMBps}
                        onChange={(e) => {
                          const val = parseFloat(e.target.value);
                          if (!isNaN(val)) {
                            updateConfig({
                              trafficDistribution: {
                                ...trafficDist,
                                model: 'advanced',
                                baseRateMBps: Math.max(0, Math.min(1000, val)),
                              }
                            });
                          }
                        }}
                        disabled={!isConnected || isRunning}
                        className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                      />
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <label className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0">
                        Burstiness
                        <div className="group relative">
                          <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                          <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                            How much faster traffic is during bursts compared to baseline. For example, 2.0 means traffic doubles during burst periods (ON state).
                          </div>
                        </div>
                      </label>
                      <input
                        type="number"
                        min={1.0}
                        max={10.0}
                        
                        value={burstMultiplier}
                        onChange={(e) => {
                          const val = parseFloat(e.target.value);
                          if (!isNaN(val)) {
                            updateConfig({
                              trafficDistribution: {
                                ...trafficDist,
                                model: 'advanced',
                                burstMultiplier: Math.max(1.0, Math.min(10.0, val)),
                              }
                            });
                          }
                        }}
                        disabled={!isConnected || isRunning}
                        className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                      />
                    </div>
                  </>
                )}
                <ConfigInput label="Deduplication Factor" field="compactionReductionFactor" min={0.1} max={1.0}
                  tooltip="Data reduction during compaction (0.9 = 10% reduction)" />
              </div>
              
              {/* Overlap Distribution Controls */}
              <div className="mt-3 pb-3 border-b border-dark-border">
                <div className="flex items-center gap-3 flex-wrap">
                  <label className="text-sm text-gray-300 flex items-center gap-1">
                    Overlap Distribution:
                    <div className="group relative">
                      <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                      <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-80 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                        Controls how many overlapping files are selected from the target level during compaction. This affects write amplification and compaction size. The overlap pattern depends on your workload: uniform writes create many overlaps, skewed workloads create fewer. Uniform: equal probability for any overlap count. Geometric: favors fewer overlaps (good for balanced workloads). Exponential: strongly favors fewer overlaps (good for skewed workloads).
                      </div>
                    </div>
                  </label>
                  <select
                    value={overlapDistType}
                    onChange={(e) => {
                      try {
                        if (!isConnected || isRunning) return;
                        const newType = e.target.value as "uniform" | "exponential" | "geometric";
                        console.log('[OverlapDist] Changing type to:', newType, 'current overlapDist:', overlapDist);
                        
                        // Ensure we have a valid overlapDist object with all required fields
                        const currentOverlapDist = overlapDist || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5 };
                        
                        // Create new overlap distribution config
                        const newOverlapDist: { type: "uniform" | "exponential" | "geometric"; geometricP?: number; exponentialLambda?: number } = {
                          type: newType,
                          // Preserve existing parameters (they're optional, so only include if they exist)
                          ...(currentOverlapDist.geometricP !== undefined && { geometricP: currentOverlapDist.geometricP }),
                          ...(currentOverlapDist.exponentialLambda !== undefined && { exponentialLambda: currentOverlapDist.exponentialLambda }),
                        };
                        
                        // Ensure defaults are set for the selected type
                        if (newType === 'geometric' && newOverlapDist.geometricP === undefined) {
                          newOverlapDist.geometricP = 0.3;
                        }
                        if (newType === 'exponential' && newOverlapDist.exponentialLambda === undefined) {
                          newOverlapDist.exponentialLambda = 0.5;
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
                    className="px-3 py-1.5 bg-dark-bg border border-dark-border rounded text-sm text-gray-300 disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                  >
                    <option value="uniform">Uniform</option>
                    <option value="exponential">Exponential</option>
                    <option value="geometric">Geometric</option>
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
                </div>
              </div>
              
              {/* Advanced Traffic Parameters (collapsible) */}
              {trafficModel === 'advanced' && (
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
                            ON Mean Duration (s)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean duration parameter for burst periods (ON state). Burst durations follow an Erlang distribution with this mean, so actual durations vary around this value. Higher values = longer bursts on average.
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
                                  onMeanSeconds: val,
                                }
                              });
                            }}
                            min={0.1}
                            max={100.0}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            OFF Mean Duration (s)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean duration parameter for quiet periods (OFF state). Quiet periods follow an Exponential distribution with this mean, so actual durations vary. Higher values = longer quiet periods on average.
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
                                  offMeanSeconds: val,
                                }
                              });
                            }}
                            min={0.1}
                            max={100.0}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Erlang K
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Controls how predictable burst durations are. Lower values (1-2) = more variable burst lengths. Higher values (5-10) = more consistent, predictable burst durations.
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
                                  erlangK: val,
                                }
                              });
                            }}
                            min={1}
                            max={10}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Rate (per sec)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Poisson arrival rate for traffic spikes (events per second). Spikes arrive randomly following a Poisson process. For example, 0.1 means an average of 0.1 spikes per second (one spike every 10 seconds on average). Set to 0 to disable spikes.
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
                                  spikeRatePerSec: val,
                                }
                              });
                            }}
                            min={0}
                            max={10.0}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Mean Duration (s)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean duration parameter for spike events. Spike durations follow an Exponential distribution with this mean, so actual spike durations vary around this value. Higher values = longer spikes on average.
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
                                  spikeMeanDur: val,
                                }
                              });
                            }}
                            min={0.1}
                            max={100.0}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Amplitude Mean
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Mean parameter (in log space) for spike amplitudes. Spike amplitudes follow a Lognormal distribution with this mean, so actual spike sizes vary. Higher values (2.0-10.0) = larger spikes on average. Lower values (0.1-1.0) = smaller spikes on average. Typical range: 1.0-3.0.
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
                                  spikeAmplitudeMean: val,
                                }
                              });
                            }}
                            min={0.1}
                            max={10.0}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Spike Amplitude Sigma
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Variance parameter (in log space) for spike amplitude distribution. Controls how much spike sizes vary. Lower values (0.01-0.5) = more consistent spike sizes. Higher values (0.5-2.0) = more variable spike sizes (mix of small and large spikes).
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
                                  spikeAmplitudeSigma: val,
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
                            Capacity Limit (MB/s)
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Maximum throughput capacity in MB/s. When traffic exceeds this limit, excess is either dropped or queued based on Queue Mode. Set to 0 for unlimited capacity.
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
                                  capacityLimitMB: val,
                                }
                              });
                            }}
                            min={0}
                            max={10000}
                            
                            disabled={!isConnected || isRunning}
                            className="w-28 px-3 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                          />
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <label className="text-xs text-gray-400 flex items-center gap-1">
                            Queue Mode
                            <div className="group relative">
                              <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                              <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                                Drop: Discard excess traffic when capacity is exceeded (simulates network throttling). Queue: Buffer excess traffic and process it later when capacity is available (simulates buffering).
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
                            className="w-28 px-2 py-1 bg-dark-bg border border-dark-border rounded text-xs text-gray-300 disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
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
