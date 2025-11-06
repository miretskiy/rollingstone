import { create } from 'zustand';
import type {
    SimulationConfig,
    SimulationMetrics,
    SimulationState,
    SimulationEvent,
    WSMessage,
    ConnectionStatus,
} from './types';

const CONFIG_STORAGE_KEY = 'rollingstone-config';

// Load configuration from localStorage
function loadConfigFromStorage(): Partial<SimulationConfig> | null {
    try {
        const stored = localStorage.getItem(CONFIG_STORAGE_KEY);
        if (!stored) return null;
        return JSON.parse(stored);
    } catch (error) {
        console.warn('Failed to load config from localStorage:', error);
        return null;
    }
}

// Save configuration to localStorage
function saveConfigToStorage(config: SimulationConfig): void {
    try {
        localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    } catch (error) {
        console.warn('Failed to save config to localStorage:', error);
    }
}

// ==== STORE ====

interface AppStore {
    // Connection
    connectionStatus: ConnectionStatus;
    ws: WebSocket | null;

    // Simulation state
    isRunning: boolean;
    config: SimulationConfig;
    currentMetrics: SimulationMetrics | null;
    metricsHistory: SimulationMetrics[];
    currentState: SimulationState | null;
    events: SimulationEvent[];
    logs: string[];

    // Actions
    connect: (url: string) => void;
    disconnect: () => void;
    sendMessage: (message: WSMessage) => void;
    start: () => void;
    pause: () => void;
    reset: () => void;
    step: () => void;
    updateConfig: (config: Partial<SimulationConfig>) => void;

    // Internal
    handleMessage: (data: string) => void;
    setConnectionStatus: (status: ConnectionStatus) => void;
}

// Default configuration
const defaultConfig: SimulationConfig = {
    writeRateMBps: 10,
    memtableFlushSizeMB: 64,
    maxWriteBufferNumber: 2,
    memtableFlushTimeoutSec: 300,
    l0CompactionTrigger: 4,
    maxBytesForLevelBaseMB: 256,
    levelMultiplier: 10,
    targetFileSizeMB: 64,
    targetFileSizeMultiplier: 2,
    compactionReductionFactor: 0.9,
    maxBackgroundJobs: 2,
    maxSubcompactions: 1,
    maxCompactionBytesMB: 1600,
    ioLatencyMs: 1,
    ioThroughputMBps: 125,
    numLevels: 7,
    initialLSMSizeMB: 0,
    simulationSpeedMultiplier: 1,
    randomSeed: 0,
    maxStalledWriteMemoryMB: 4096, // 4GB default OOM threshold
    compactionStyle: 'universal', // Default to universal compaction
    maxSizeAmplificationPercent: 200, // Default RocksDB value
    levelCompactionDynamicLevelBytes: false, // Default false when compactionStyle is universal
    trafficDistribution: {
        model: 'constant',
        writeRateMBps: 10.0,
    },
    overlapDistribution: {
        type: 'geometric',
        geometricP: 0.3,
        exponentialLambda: 0.5,
    },
};

// Load initial config from localStorage, merging with defaults
function getInitialConfig(): SimulationConfig {
    const stored = loadConfigFromStorage();
    if (!stored) return defaultConfig;
    
    // Deep merge stored config with defaults to handle new fields
    const mergedConfig: SimulationConfig = {
        ...defaultConfig,
        ...stored,
        trafficDistribution: stored.trafficDistribution 
            ? {
                ...defaultConfig.trafficDistribution!,
                ...stored.trafficDistribution,
                model: stored.trafficDistribution.model || defaultConfig.trafficDistribution!.model,
            }
            : defaultConfig.trafficDistribution!,
        overlapDistribution: stored.overlapDistribution
            ? {
                ...defaultConfig.overlapDistribution!,
                ...stored.overlapDistribution,
                type: stored.overlapDistribution.type || defaultConfig.overlapDistribution!.type,
            }
            : defaultConfig.overlapDistribution!,
    };
    
    return mergedConfig;
}

export const useStore = create<AppStore>((set, get) => ({
    // Initial state
    connectionStatus: 'disconnected',
    ws: null,
    isRunning: false,
    config: getInitialConfig(),
    currentMetrics: null,
    metricsHistory: [],
    currentState: null,
    events: [],
    logs: [],

    // Connection management
    connect: (url: string) => {
        const { ws, disconnect } = get();

        // Close existing connection
        if (ws) {
            disconnect();
        }

        set({ connectionStatus: 'connecting' });

        try {
            const newWs = new WebSocket(url);

            newWs.onopen = () => {
                console.log('WebSocket connected');
                set({ connectionStatus: 'connected', ws: newWs });
            };

            newWs.onclose = () => {
                console.log('WebSocket disconnected');
                set({ connectionStatus: 'disconnected', ws: null });
            };

            newWs.onerror = (error) => {
                console.error('WebSocket error:', error);
                set({ connectionStatus: 'error' });
            };

            newWs.onmessage = (event) => {
                get().handleMessage(event.data);
            };

            set({ ws: newWs });
        } catch (error) {
            console.error('Failed to create WebSocket:', error);
            set({ connectionStatus: 'error' });
        }
    },

    disconnect: () => {
        const { ws } = get();
        if (ws) {
            ws.close();
            set({ ws: null, connectionStatus: 'disconnected' });
        }
    },

    sendMessage: (message: WSMessage) => {
        const { ws, connectionStatus } = get();
        if (ws && connectionStatus === 'connected') {
            ws.send(JSON.stringify(message));
        } else {
            console.warn('Cannot send message: WebSocket not connected');
        }
    },

    // Simulation controls
    start: () => {
        get().sendMessage({ type: 'start' });
        set({ isRunning: true });
    },

    pause: () => {
        get().sendMessage({ type: 'pause' });
        set({ isRunning: false });
    },

    reset: () => {
        get().sendMessage({ type: 'reset' });
        set({
            isRunning: false,
            metricsHistory: [],
            events: [],
            logs: [],
            currentMetrics: null,
            currentState: null,
        });
    },

    step: () => {
        get().sendMessage({ type: 'step' });
    },

    updateConfig: (configUpdate: Partial<SimulationConfig>) => {
        const currentConfig = get().config;
        const newConfig = { ...currentConfig };
        
        // Deep merge nested configs
        if (configUpdate.trafficDistribution) {
            newConfig.trafficDistribution = {
                ...(currentConfig.trafficDistribution || { model: 'constant', writeRateMBps: currentConfig.writeRateMBps }),
                ...configUpdate.trafficDistribution,
            };
        }
        
        if (configUpdate.overlapDistribution) {
            newConfig.overlapDistribution = {
                ...(currentConfig.overlapDistribution || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5 }),
                ...configUpdate.overlapDistribution,
            };
        }
        
        // Merge top-level config
        Object.assign(newConfig, configUpdate);
        
        // Automatically disable levelCompactionDynamicLevelBytes when compaction style is universal
        if (newConfig.compactionStyle === 'universal') {
            newConfig.levelCompactionDynamicLevelBytes = false;
        }
        // Automatically enable levelCompactionDynamicLevelBytes when compaction style is leveled (RocksDB default)
        else if (newConfig.compactionStyle === 'leveled' && configUpdate.compactionStyle === 'leveled') {
            // Only enable if explicitly switching to leveled (not if it was already leveled)
            if (currentConfig.compactionStyle !== 'leveled') {
                newConfig.levelCompactionDynamicLevelBytes = true;
            }
        }
        
        // Save to localStorage
        saveConfigToStorage(newConfig);
        
        // Send the FULL config to backend (it expects complete SimConfig)
        get().sendMessage({ type: 'config_update', config: newConfig });
        set({ config: newConfig });
    },

    // Message handling
    handleMessage: (data: string) => {
        try {
            const message: WSMessage = JSON.parse(data);
            // Debug: uncomment to see all messages (causes browser slowdown if running long)
            // console.log('📨 Received message:', message.type, message);

            switch (message.type) {
                case 'status':
                    // console.log('Status update:', message);
                    // Ensure overlapDistribution has defaults if missing
                    const statusConfig = message.config;
                    if (statusConfig && !statusConfig.overlapDistribution) {
                        statusConfig.overlapDistribution = {
                            type: 'geometric',
                            geometricP: 0.3,
                            exponentialLambda: 0.5,
                        };
                    }
                    if (statusConfig) {
                        // Save config from server to localStorage (in case server was restarted)
                        saveConfigToStorage(statusConfig);
                    }
                    set({
                        isRunning: message.running,
                        config: statusConfig,
                    });
                    break;

                case 'metrics':
                    // Commented out to prevent console spam (20 msgs/sec)
                    // console.log('Metrics update:', message.metrics);
                    set((state) => ({
                        currentMetrics: message.metrics,
                        metricsHistory: [...state.metricsHistory, message.metrics].slice(-500), // Increased to 500 for better charts
                    }));
                    break;

                case 'state':
                    // console.log('State update:', message.state);
                    set({ currentState: message.state });
                    break;

                case 'event':
                    // console.log('Event:', message.event);
                    set((state) => ({
                        events: [message.event, ...state.events].slice(0, 50),
                    }));
                    break;

                case 'log':
                    // Handle batched log messages (may contain multiple lines)
                    if (message.log) {
                        const logLines = message.log.split('\n').filter(line => line.trim());
                        set((state) => {
                            const newLogs = [...state.logs, ...logLines];
                            // Keep ring buffer of 1000 entries
                            return { logs: newLogs.slice(-1000) };
                        });
                    }
                    break;

                default:
                    console.warn('Unknown message type:', message.type);
            }
        } catch (error) {
            console.error('Failed to parse WebSocket message:', error, 'Data:', data);
        }
    },

    setConnectionStatus: (status: ConnectionStatus) => {
        set({ connectionStatus: status });
    },
}));
