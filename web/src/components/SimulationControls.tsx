import { useState } from 'react';
import { Play, Pause, RotateCcw, Settings, ChevronDown, ChevronRight } from 'lucide-react';
import { useStore } from '../store';
import type { SimulationConfig } from '../types';
import { ConfigInput } from './ConfigInput';

export function SimulationControls() {
  const { connectionStatus, isRunning, start, pause, reset, updateConfig } = useStore();
  // Read current I/O config for preset selection
  const ioLatency = useStore(state => state.config.ioLatencyMs);
  const ioThroughput = useStore(state => state.config.ioThroughputMBps);
  const [expandedSections, setExpandedSections] = useState({
    lsm: true,
    lsmAdvanced: false,
    workload: true,
    io: false,
    simulation: false,
  });

  const isConnected = connectionStatus === 'connected';

  const toggleSection = (section: keyof typeof expandedSections) => {
    setExpandedSections(prev => ({ ...prev, [section]: !prev[section] }));
  };

  // ConfigInput now reads directly from store, so just pass it through
  const Input = ConfigInput;

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
        </div>

        <div className="flex items-center gap-2">
          <div className="flex items-center gap-2 mr-4">
            <div className={`w-2 h-2 rounded-full ${isConnected ? 'bg-green-500 animate-pulse' : 'bg-gray-500'}`} />
            <span className="text-sm text-gray-400 capitalize">{connectionStatus}</span>
          </div>

          <button
            onClick={isRunning ? pause : start}
            disabled={!isConnected}
            className="flex items-center gap-2 px-6 py-3 bg-primary-600 hover:bg-primary-700 disabled:bg-gray-600 disabled:cursor-not-allowed rounded-lg font-semibold transition-all transform hover:scale-105 active:scale-95"
          >
            {isRunning ? <><Pause className="w-5 h-5" />Pause</> : <><Play className="w-5 h-5" />Play</>}
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
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <Input label="Memtable Flush Size" field="memtableFlushSizeMB" min={1} max={512} unit="MB"
                  tooltip="Size at which memtable is flushed to L0" />
                <Input label="Max Immutable Memtables" field="maxWriteBufferNumber" min={1} max={10}
                  tooltip="Max number of memtables before write stall" />
                <Input label="L0 Compaction Trigger" field="l0CompactionTrigger" min={2} max={20} unit="files"
                  tooltip="Number of L0 files that trigger compaction" />
                <Input label="Level Size Multiplier" field="levelMultiplier" min={2} max={100}
                  tooltip="Size multiplier between levels (default: 10)" />
                <Input label="Compaction Parallelism" field="maxBackgroundJobs" min={1} max={32}
                  tooltip="Max concurrent compaction jobs" />
                <Input label="Number of Levels" field="numLevels" min={2} max={10}
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
                      <Input label="Max Bytes for Level Base" field="maxBytesForLevelBaseMB" min={64} max={2048} unit="MB"
                        tooltip="Target size for L1 (RocksDB: max_bytes_for_level_base)" />
                      <Input label="Target SST File Size" field="targetFileSizeMB" min={1} max={512} unit="MB"
                        tooltip="Target size for individual SST files (RocksDB: target_file_size_base)" />
                      <Input label="File Size Multiplier" field="targetFileSizeMultiplier" min={1} max={10}
                        tooltip="SST file size multiplier per level (RocksDB: target_file_size_multiplier)" />
                      <Input label="Max Compaction Bytes" field="maxCompactionBytesMB" min={100} max={10000} unit="MB"
                        tooltip="Max total input size for single compaction (RocksDB: max_compaction_bytes)" />
                      <Input label="Max Subcompactions" field="maxSubcompactions" min={1} max={16}
                        tooltip="Parallelism within a single compaction job (RocksDB: max_subcompactions)" />
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
              <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                <Input label="Write Rate" field="writeRateMBps" min={0} max={1000} unit="MB/s"
                  tooltip="Incoming write throughput (0 = no writes)" />
                <Input label="Deduplication Factor" field="compactionReductionFactor" min={0.1} max={1.0}
                  tooltip="Data reduction during compaction (0.9 = 10% reduction)" />
              </div>
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
                  label="SATA SSD"
                  onClick={() => { handleConfigChange('ioLatencyMs', 0.5); handleConfigChange('ioThroughputMBps', 550); }}
                  disabled={!isConnected || isRunning}
                  isSelected={Math.abs(ioLatency - 0.5) < 0.01 && Math.abs(ioThroughput - 550) < 1}
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
                <Input label="I/O Latency" field="ioLatencyMs" min={0.1} max={50} unit="ms"
                  tooltip="Disk operation latency" />
                <Input label="I/O Throughput" field="ioThroughputMBps" min={10} max={10000} unit="MB/s"
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
                <Input label="Simulation Speed" field="simulationSpeedMultiplier" min={1} max={100} unit="x"
                  tooltip="Speed multiplier for fast-forward simulation" />
                <Input label="Initial LSM Size" field="initialLSMSizeMB" min={0} max={100000} unit="MB"
                  tooltip="⚠️ Pre-populate LSM tree (requires reset)" />
                <Input label="Random Seed" field="randomSeed" min={0} max={999999}
                  tooltip="Random seed for reproducibility (0 = random)" />
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
