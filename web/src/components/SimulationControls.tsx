import { useState } from 'react';
import { Play, Pause, RotateCcw, Settings, ChevronDown, ChevronRight } from 'lucide-react';
import { useStore } from '../store';
import type { SimulationConfig } from '../types';

export function SimulationControls() {
  const { connectionStatus, isRunning, start, pause, reset, updateConfig, config } = useStore();
  const [expandedSections, setExpandedSections] = useState({
    lsm: true,        // Start expanded for visibility
    workload: true,   // Start expanded for visibility
    io: false,
    advanced: false,
  });

  const isConnected = connectionStatus === 'connected';

  const toggleSection = (section: keyof typeof expandedSections) => {
    setExpandedSections(prev => ({ ...prev, [section]: !prev[section] }));
  };

  const getLSMSummary = () => {
    if (!config) return '';
    const isDefault = config.numLevels === 7 && config.memtableFlushSizeMB === 64 &&
      config.l0CompactionTrigger === 4 && config.compactionReductionFactor === 0.9;
    if (isDefault) return 'Default (7 levels, 64MB memtable, 4-file L0 trigger)';
    return `${config.numLevels} levels, ${config.memtableFlushSizeMB}MB memtable, L0 trigger: ${config.l0CompactionTrigger} files`;
  };

  const getWorkloadSummary = () => {
    if (!config) return '';
    return `Constant write rate: ${config.writeRateMBps} MB/s`;
  };

  const getIOSummary = () => {
    if (!config) return '';
    // Detect preset
    if (config.ioLatencyMs === 0.1 && config.ioThroughputMBps === 3000) return 'NVMe SSD';
    if (config.ioLatencyMs === 0.2 && config.ioThroughputMBps === 500) return 'SATA SSD';
    if (config.ioLatencyMs === 12 && config.ioThroughputMBps === 150) return 'HDD (7200 RPM)';
    if (config.ioLatencyMs === 3 && config.ioThroughputMBps === 250) return 'EBS gp3';
    return `${config.ioLatencyMs}ms latency, ${config.ioThroughputMBps} MB/s`;
  };

  const handleConfigChange = (field: keyof SimulationConfig, value: number) => {
    updateConfig({ [field]: value });
  };

  // Determine if a parameter can be changed while running
  const isStaticParam = (field: keyof SimulationConfig): boolean => {
    // Only writeRateMBps can be adjusted while running
    return field !== 'writeRateMBps';
  };

  // Check if config controls should be disabled
  const isConfigDisabled = (field: keyof SimulationConfig): boolean => {
    return isRunning && isStaticParam(field);
  };

  return (
    <div className="bg-dark-card border border-dark-border rounded-lg p-6 shadow-xl">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-2">
          <Settings className="w-5 h-5 text-primary-400" />
          <h2 className="text-xl font-bold">Simulation Controls</h2>
        </div>

        <div className="flex items-center gap-2">
          {/* Connection Status */}
          <div className="flex items-center gap-2 mr-4">
            <div className={`w-2 h-2 rounded-full ${isConnected ? 'bg-green-500 animate-pulse' : 'bg-gray-500'}`} />
            <span className="text-sm text-gray-400 capitalize">{connectionStatus}</span>
          </div>

          {/* Playback Controls */}
          <button
            onClick={isRunning ? pause : start}
            disabled={!isConnected}
            className="flex items-center gap-2 px-6 py-3 bg-primary-600 hover:bg-primary-700 disabled:bg-gray-600 disabled:cursor-not-allowed rounded-lg font-semibold transition-all transform hover:scale-105 active:scale-95"
          >
            {isRunning ? (
              <>
                <Pause className="w-5 h-5" />
                Pause
              </>
            ) : (
              <>
                <Play className="w-5 h-5" />
                Play
              </>
            )}
          </button>

          <button
            onClick={reset}
            disabled={!isConnected}
            className="p-3 bg-dark-bg hover:bg-gray-700 disabled:bg-gray-800 disabled:cursor-not-allowed rounded-lg transition-all transform hover:scale-105 active:scale-95"
            title="Reset"
          >
            <RotateCcw className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Core Configuration - Always Visible */}
      {config && (
        <div className="space-y-6">
          {/* LSM Configuration Section */}
          <div className="bg-linear-to-r from-primary-900/20 to-purple-900/20 border border-primary-700/30 rounded-lg overflow-hidden">
            <button
              onClick={() => toggleSection('lsm')}
              className="w-full px-4 py-3 flex items-center justify-between hover:bg-primary-900/10 transition-colors"
            >
              <div className="flex items-center gap-2">
                <span className="w-1 h-5 bg-primary-400 rounded"></span>
                <h3 className="text-base font-bold text-primary-300">LSM Tree Configuration</h3>
                <span className="text-sm text-gray-400">— {getLSMSummary()}</span>
                {isRunning && <span className="text-xs px-2 py-0.5 bg-gray-700 text-gray-400 rounded">🔒 Locked while running</span>}
              </div>
              {expandedSections.lsm ? <ChevronDown className="w-4 h-4 text-primary-400" /> : <ChevronRight className="w-4 h-4 text-primary-400" />}
            </button>
            {expandedSections.lsm && (
              <div className="px-4 pb-4 pt-2">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <ConfigSlider
                    label="Number of Levels"
                    value={config.numLevels}
                    min={2}
                    max={10}
                    step={1}
                    onChange={(v) => handleConfigChange('numLevels', v)}
                    hint="How many levels in the LSM tree. More levels = higher space efficiency but slower reads. RocksDB default: 7 levels"
                    disabled={isConfigDisabled('numLevels')}
                  />
                  <ConfigSlider
                    label="Memtable Size (MB)"
                    value={config.memtableFlushSizeMB}
                    min={16}
                    max={512}
                    step={16}
                    onChange={(v) => handleConfigChange('memtableFlushSizeMB', v)}
                    hint="Size of in-memory buffer before flushing to L0. Larger = fewer flushes but more memory. RocksDB default: 64MB (write_buffer_size)"
                    disabled={isConfigDisabled('memtableFlushSizeMB')}
                  />
                  <ConfigSlider
                    label="L0 Compaction Trigger"
                    value={config.l0CompactionTrigger}
                    min={2}
                    max={16}
                    step={1}
                    onChange={(v) => handleConfigChange('l0CompactionTrigger', v)}
                    hint="Number of L0 files before compacting into L1. Lower = less read amplification but more write I/O. RocksDB default: 4 (level0_file_num_compaction_trigger)"
                    disabled={isConfigDisabled('l0CompactionTrigger')}
                  />
                  <ConfigSlider
                    label="L0→L1 Deduplication"
                    value={config.compactionReductionFactor}
                    min={0.5}
                    max={1.0}
                    step={0.05}
                    onChange={(v) => handleConfigChange('compactionReductionFactor', v)}
                    hint="Percentage of duplicate writes to same keys (simulates updates/deletes). Higher % = more data reduction during L0→L1 compaction. Deeper levels use 1%."
                    formatValue={(v) => `${((1 - v) * 100).toFixed(0)}%`}
                    disabled={isConfigDisabled('compactionReductionFactor')}
                  />
                </div>
                {/* LSM Presets */}
                <div className="mt-4 pt-4 border-t border-primary-700/30 flex gap-2">
                  <button
                    onClick={() => {
                      updateConfig({
                        numLevels: 7,
                        memtableFlushSizeMB: 64,
                        maxWriteBufferNumber: 2,
                        memtableFlushTimeoutSec: 300,
                        l0CompactionTrigger: 4,
                        maxBytesForLevelBaseMB: 256,
                        levelMultiplier: 10,
                        compactionReductionFactor: 0.9,
                        maxBackgroundJobs: 2,
                        maxSubcompactions: 1,
                        writeRateMBps: 10,
                        ioLatencyMs: 5,
                        ioThroughputMBps: 500,
                      });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-sm rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-primary-600 hover:bg-primary-700'
                      }`}
                  >
                    Default (7 levels)
                  </button>
                  <button
                    onClick={() => {
                      updateConfig({
                        numLevels: 3,
                        memtableFlushSizeMB: 64,
                        maxWriteBufferNumber: 2,
                        memtableFlushTimeoutSec: 60,
                        l0CompactionTrigger: 4,
                        maxBytesForLevelBaseMB: 256,
                        levelMultiplier: 10,
                        compactionReductionFactor: 0.9,
                        maxBackgroundJobs: 2,
                        maxSubcompactions: 1,
                        writeRateMBps: 10,
                        ioLatencyMs: 5,
                        ioThroughputMBps: 500,
                      });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-sm rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-orange-600 hover:bg-orange-700'
                      }`}
                  >
                    Simple (3 levels)
                  </button>
                </div>
              </div>
            )}
          </div>

          {/* Workload Configuration Section */}
          <div className="bg-linear-to-r from-orange-900/20 to-red-900/20 border border-orange-700/30 rounded-lg overflow-hidden">
            <button
              onClick={() => toggleSection('workload')}
              className="w-full px-4 py-3 flex items-center justify-between hover:bg-orange-900/10 transition-colors"
            >
              <div className="flex items-center gap-2">
                <span className="w-1 h-5 bg-orange-400 rounded"></span>
                <h3 className="text-base font-bold text-orange-300">Workload & Traffic Patterns</h3>
                <span className="text-sm text-gray-400">— {getWorkloadSummary()}</span>
                {isRunning && <span className="text-xs px-2 py-0.5 bg-green-700 text-green-200 rounded">✨ Live adjustable</span>}
              </div>
              {expandedSections.workload ? <ChevronDown className="w-4 h-4 text-orange-400" /> : <ChevronRight className="w-4 h-4 text-orange-400" />}
            </button>
            {expandedSections.workload && (
              <div className="px-4 pb-4 pt-2">
                <div className="space-y-4">
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <label className="text-sm font-medium text-gray-300">Write Pattern</label>
                      <select
                        className="w-full px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-gray-100 focus:border-orange-500 focus:outline-none"
                        disabled
                      >
                        <option>Constant Rate</option>
                        <option>Poisson Distribution (coming soon)</option>
                        <option>Bursty (coming soon)</option>
                        <option>YCSB Workload A (coming soon)</option>
                        <option>YCSB Workload B (coming soon)</option>
                      </select>
                      <p className="text-xs text-gray-500">Traffic pattern for writes</p>
                    </div>
                    <ConfigSlider
                      label="Write Rate (MB/s)"
                      value={config.writeRateMBps}
                      min={0}
                      max={1000}
                      step={0.1}
                      onChange={(v) => handleConfigChange('writeRateMBps', v)}
                      hint="Incoming write traffic in MB/s. This is the ONLY parameter you can adjust while the simulation is running. Set to 0 to pause writes."
                      disabled={false}
                    />
                  </div>
                  <div className="bg-dark-bg/50 rounded-lg p-3 border border-gray-700">
                    <p className="text-sm text-gray-400">
                      <strong>Note:</strong> Currently simulating constant write rate. Future versions will support:
                    </p>
                    <ul className="text-xs text-gray-500 mt-2 ml-4 space-y-1">
                      <li>• <strong>Poisson Distribution</strong> - Random arrivals with λ parameter</li>
                      <li>• <strong>Bursty Traffic</strong> - Periodic spikes with configurable intensity</li>
                      <li>• <strong>YCSB Workloads</strong> - Industry-standard benchmarks (A: 50/50 read/write, B: 95/5 read/write, etc.)</li>
                      <li>• <strong>Read Operations</strong> - Point lookups, range scans with configurable distributions</li>
                    </ul>
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* I/O Profile Section */}
          <div className="bg-linear-to-r from-purple-900/20 to-pink-900/20 border border-purple-700/30 rounded-lg overflow-hidden">
            <button
              onClick={() => toggleSection('io')}
              className="w-full px-4 py-3 flex items-center justify-between hover:bg-purple-900/10 transition-colors"
            >
              <div className="flex items-center gap-2">
                <span className="w-1 h-5 bg-purple-400 rounded"></span>
                <h3 className="text-base font-bold text-purple-300">I/O Hardware Profile</h3>
                <span className="text-sm text-gray-400">— {getIOSummary()}</span>
                {isRunning && <span className="text-xs px-2 py-0.5 bg-gray-700 text-gray-400 rounded">🔒 Locked while running</span>}
              </div>
              {expandedSections.io ? <ChevronDown className="w-4 h-4 text-purple-400" /> : <ChevronRight className="w-4 h-4 text-purple-400" />}
            </button>
            {expandedSections.io && (
              <div className="px-4 pb-4 pt-2">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <ConfigSlider
                    label="I/O Latency (ms)"
                    value={config.ioLatencyMs}
                    min={0.1}
                    max={100}
                    step={0.1}
                    onChange={(v) => handleConfigChange('ioLatencyMs', v)}
                    hint="Random access latency (seek time). NVMe: 0.1ms, SATA SSD: 0.2ms, HDD: 12ms. Lower = faster random I/O."
                    disabled={isConfigDisabled('ioLatencyMs')}
                  />
                  <ConfigSlider
                    label="I/O Throughput (MB/s)"
                    value={config.ioThroughputMBps}
                    min={10}
                    max={5000}
                    step={10}
                    onChange={(v) => handleConfigChange('ioThroughputMBps', v)}
                    hint="Sequential read/write speed. SATA SSD: ~500 MB/s, NVMe: ~3000 MB/s, HDD: ~150 MB/s. Higher = faster compactions and flushes."
                    disabled={isConfigDisabled('ioThroughputMBps')}
                  />
                  <ConfigSlider
                    label="Compaction Parallelism"
                    value={config.maxBackgroundJobs}
                    min={1}
                    max={8}
                    step={1}
                    onChange={(v) => handleConfigChange('maxBackgroundJobs', v)}
                    hint="How many compactions can run simultaneously. 1 = sequential (slow), 2-4 = typical, 6+ = aggressive. More parallelism = higher disk I/O. RocksDB default: 2 (max_background_jobs)"
                    disabled={isConfigDisabled('maxBackgroundJobs')}
                  />
                </div>
                <div className="mt-3 flex gap-2">
                  <button
                    onClick={() => {
                      updateConfig({ ioLatencyMs: 0.1, ioThroughputMBps: 3000 });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-xs rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-purple-600 hover:bg-purple-700'
                      }`}
                  >
                    NVMe SSD
                  </button>
                  <button
                    onClick={() => {
                      updateConfig({ ioLatencyMs: 0.2, ioThroughputMBps: 500 });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-xs rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-purple-600 hover:bg-purple-700'
                      }`}
                  >
                    SATA SSD
                  </button>
                  <button
                    onClick={() => {
                      updateConfig({ ioLatencyMs: 12, ioThroughputMBps: 150 });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-xs rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-purple-600 hover:bg-purple-700'
                      }`}
                    title="7200 RPM HDD - typical seek + rotational latency"
                  >
                    HDD (7200 RPM)
                  </button>
                  <button
                    onClick={() => {
                      updateConfig({ ioLatencyMs: 3, ioThroughputMBps: 250 });
                    }}
                    disabled={isRunning}
                    className={`px-3 py-1.5 text-xs rounded transition-colors ${isRunning
                      ? 'bg-gray-700 text-gray-500 cursor-not-allowed'
                      : 'bg-purple-600 hover:bg-purple-700'
                      }`}
                    title="AWS EBS gp3 - typical configuration"
                  >
                    EBS gp3
                  </button>
                </div>
              </div>
            )}
          </div>

          {/* Advanced LSM Configuration - Collapsed by Default */}
          <div className="border-t border-dark-border pt-4">
            <button
              onClick={() => toggleSection('advanced')}
              className="w-full flex items-center justify-between px-4 py-2 bg-dark-bg hover:bg-gray-700 rounded-lg transition-colors"
            >
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold text-gray-400">Advanced LSM Tuning</span>
                {isRunning && <span className="text-xs px-2 py-0.5 bg-gray-700 text-gray-400 rounded">🔒 Locked while running</span>}
              </div>
              {expandedSections.advanced ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
            </button>

            {expandedSections.advanced && (
              <div className="mt-4 space-y-4 p-4 bg-dark-bg rounded-lg border border-dark-border">
                <p className="text-xs text-gray-500 mb-4">
                  Fine-tune RocksDB parameters for advanced use cases. Most users should stick with defaults.
                </p>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <ConfigSlider
                    label="Max Write Buffers"
                    value={config.maxWriteBufferNumber}
                    min={1}
                    max={8}
                    step={1}
                    onChange={(v) => handleConfigChange('maxWriteBufferNumber', v)}
                    hint="max_write_buffer_number - buffers before stalling (default: 2)"
                    disabled={isConfigDisabled('maxWriteBufferNumber')}
                  />
                  <ConfigSlider
                    label="Flush Timeout (sec)"
                    value={config.memtableFlushTimeoutSec}
                    min={0}
                    max={600}
                    step={30}
                    onChange={(v) => handleConfigChange('memtableFlushTimeoutSec', v)}
                    hint="Flush memtable after this many seconds, even if not full. Prevents data loss and reduces recovery time. 0 = disabled (only size-based flushes)."
                    disabled={isConfigDisabled('memtableFlushTimeoutSec')}
                  />
                  <ConfigSlider
                    label="L1 Base Size (MB)"
                    value={config.maxBytesForLevelBaseMB}
                    min={64}
                    max={2048}
                    step={64}
                    onChange={(v) => handleConfigChange('maxBytesForLevelBaseMB', v)}
                    hint="Target size for L1. Each deeper level is multiplied by Level Multiplier. Larger L1 = less write amplification but higher read latency. RocksDB default: 256MB (max_bytes_for_level_base)"
                    disabled={isConfigDisabled('maxBytesForLevelBaseMB')}
                  />
                  <ConfigSlider
                    label="Level Multiplier"
                    value={config.levelMultiplier}
                    min={2}
                    max={100}
                    step={1}
                    onChange={(v) => handleConfigChange('levelMultiplier', v)}
                    hint="Growth factor between levels. L2 = L1 × multiplier, L3 = L2 × multiplier, etc. Higher = fewer levels needed but higher space amp. RocksDB default: 10 (max_bytes_for_level_multiplier)"
                    disabled={isConfigDisabled('levelMultiplier')}
                  />
                  <ConfigSlider
                    label="Subcompactions"
                    value={config.maxSubcompactions}
                    min={1}
                    max={16}
                    step={1}
                    onChange={(v) => handleConfigChange('maxSubcompactions', v)}
                    hint="Split each compaction job into sub-jobs for parallel execution. Only helps for very large compactions (L0→L1). RocksDB default: 1 (max_subcompactions)"
                    disabled={isConfigDisabled('maxSubcompactions')}
                  />
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

interface ConfigSliderProps {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (value: number) => void;
  hint?: string;
  formatValue?: (value: number) => string;
  disabled?: boolean;
}

function ConfigSlider({ label, value, min, max, step, onChange, hint, formatValue, disabled = false }: ConfigSliderProps) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <label
          className={`text-sm font-medium cursor-help ${disabled ? 'text-gray-500' : 'text-gray-300'}`}
          title={hint || label}
        >
          {label}
        </label>
        <span className={`text-sm font-mono px-2 py-1 rounded ${disabled ? 'bg-gray-900 text-gray-600' : 'bg-gray-800 text-gray-200'}`}>
          {formatValue ? formatValue(value) : value}
        </span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(parseFloat(e.target.value))}
        disabled={disabled}
        className={`w-full h-2 bg-gray-700 rounded-lg appearance-none ${disabled ? 'cursor-not-allowed opacity-40' : 'cursor-pointer'} accent-primary-500`}
        title={hint || `${label}: ${formatValue ? formatValue(value) : value}`}
      />
      {hint && <p className="text-xs text-gray-500">{hint}{disabled && ' (pause to change)'}</p>}
    </div>
  );
}
