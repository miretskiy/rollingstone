import { create } from 'zustand';
import type {
    SimulationConfig,
    SimulationMetrics,
    SimulationState,
    SimulationEvent,
    WSMessage,
    ConnectionStatus,
} from './types';

const CONFIG_COOKIE_NAME = 'rollingstone-config';
const COOKIE_MAX_AGE_DAYS = 365; // Persist for 1 year

// Cookie utility functions
function setCookie(name: string, value: string, days: number): void {
    try {
        const expires = new Date();
        expires.setTime(expires.getTime() + days * 24 * 60 * 60 * 1000);
        document.cookie = `${name}=${encodeURIComponent(value)};expires=${expires.toUTCString()};path=/;SameSite=Lax`;
    } catch (error) {
        console.warn('Failed to set cookie:', error);
    }
}

function getCookie(name: string): string | null {
    try {
        const nameEQ = name + '=';
        const ca = document.cookie.split(';');
        for (let i = 0; i < ca.length; i++) {
            let c = ca[i];
            while (c.charAt(0) === ' ') c = c.substring(1, c.length);
            if (c.indexOf(nameEQ) === 0) {
                return decodeURIComponent(c.substring(nameEQ.length, c.length));
            }
        }
        return null;
    } catch (error) {
        console.warn('Failed to read cookie:', error);
        return null;
    }
}

// Load configuration from cookie
function loadConfigFromStorage(): Partial<SimulationConfig> | null {
    try {
        const stored = getCookie(CONFIG_COOKIE_NAME);
        if (!stored) return null;
        return JSON.parse(stored);
    } catch (error) {
        console.warn('Failed to load config from cookie:', error);
        return null;
    }
}

// Save configuration to cookie
function saveConfigToStorage(config: SimulationConfig): void {
    try {
        const configStr = JSON.stringify(config);
        // Cookies have a 4KB limit, so check size
        if (configStr.length > 4000) {
            console.warn('Config too large for cookie, truncating...');
            // For very large configs, we could split across multiple cookies
            // For now, just save what fits
        }
        setCookie(CONFIG_COOKIE_NAME, configStr, COOKIE_MAX_AGE_DAYS);
    } catch (error) {
        console.warn('Failed to save config to cookie:', error);
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
    resetConfig: () => void;

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
    deduplicationFactor: 0.9,
    compressionFactor: 0.7,
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
    enableWAL: true, // Enable Write-Ahead Log (RocksDB default: disableWAL=false)
    walSync: false, // Sync WAL after each write (RocksDB default: sync=false)
    walSyncLatencyMs: 1.5, // fsync() latency in milliseconds (typical for NVMe/SSD)
    trafficDistribution: {
        model: 'constant',
        writeRateMBps: 10.0,
    },
    overlapDistribution: {
        type: 'geometric',
        geometricP: 0.3,
        exponentialLambda: 0.5,
        fixedPercentage: 0.5,
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
                
                // Send saved config to server on connection
                // This ensures server uses the persisted configuration
                const currentConfig = get().config;
                console.log('[Store] Sending saved config to server on connection:', currentConfig);
                get().sendMessage({ type: 'config_update', config: currentConfig });
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
            const messageStr = JSON.stringify(message);
            console.log('[Store] Sending WebSocket message:', message.type, messageStr.length, 'bytes');
            if (message.type === 'config_update') {
                console.log('[Store] Config update message content:', messageStr);
            }
            try {
                ws.send(messageStr);
            } catch (error) {
                console.error('[Store] Error sending WebSocket message:', error);
                throw error;
            }
        } else {
            console.warn('Cannot send message: WebSocket not connected', { connectionStatus, hasWs: !!ws });
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
        try {
            console.log('[Store] updateConfig called with:', configUpdate);
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
                const currentOverlap = currentConfig.overlapDistribution || { type: 'geometric', geometricP: 0.3, exponentialLambda: 0.5, fixedPercentage: 0.5 };
                const updateOverlap = configUpdate.overlapDistribution;
                
                console.log('[Store] Merging overlapDistribution - current:', currentOverlap, 'update:', updateOverlap);
                
                // Validate type
                if (updateOverlap.type && !['uniform', 'exponential', 'geometric', 'fixed'].includes(updateOverlap.type)) {
                    throw new Error(`Invalid overlapDistribution.type: ${updateOverlap.type}. Must be 'uniform', 'exponential', 'geometric', or 'fixed'`);
                }
                
                newConfig.overlapDistribution = {
                    ...currentOverlap,
                    ...updateOverlap,
                    // Ensure type is always set
                    type: (updateOverlap.type || currentOverlap.type || 'geometric') as "uniform" | "exponential" | "geometric" | "fixed",
                };
                
                console.log('[Store] Merged overlapDistribution:', newConfig.overlapDistribution);
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
            
            // Validate final config
            if (newConfig.overlapDistribution && !newConfig.overlapDistribution.type) {
                throw new Error('overlapDistribution.type is required');
            }
            
            console.log('[Store] Final config before save:', newConfig);
            
            // Save to localStorage
            saveConfigToStorage(newConfig);
            
            // Send the FULL config to backend (it expects complete SimConfig)
            get().sendMessage({ type: 'config_update', config: newConfig });
            set({ config: newConfig });
            
            console.log('[Store] Config updated successfully');
        } catch (error) {
            console.error('[Store] Error in updateConfig:', error);
            console.error('[Store] Error stack:', error instanceof Error ? error.stack : 'No stack trace');
            console.error('[Store] Config update that failed:', configUpdate);
            alert(`Error updating configuration: ${error instanceof Error ? error.message : String(error)}\n\nCheck browser console for details.`);
            throw error; // Re-throw so caller can handle it
        }
    },

    resetConfig: () => {
        // Clear the saved config from cookie
        try {
            // Delete cookie by setting it to expire in the past
            setCookie(CONFIG_COOKIE_NAME, '', -1);
            console.log('[Store] Cleared saved config from cookie');
        } catch (error) {
            console.warn('[Store] Failed to clear config cookie:', error);
        }
        
        // Send reset_config message to server
        get().sendMessage({ type: 'reset_config' });
        
        // The server will respond with a status message containing the default config,
        // which will update the store via handleMessage
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
                    
                    // Update config from server if:
                    // 1. We don't have a saved config, OR
                    // 2. The cookie was just cleared (indicated by cookie being empty/null)
                    // This allows reset_config to work properly
                    const hasSavedConfig = loadConfigFromStorage() !== null;
                    if (statusConfig) {
                        if (!hasSavedConfig) {
                            // No saved config - use server's config and save it
                            console.log('[Store] No saved config found, using server config');
                            saveConfigToStorage(statusConfig);
                            set({
                                isRunning: message.running,
                                config: statusConfig,
                            });
                        } else {
                            // We have saved config - keep it and send it to server
                            // (Server will update on next config_update message)
                            // EXCEPT: if cookie is empty, accept server config (reset_config case)
                            const cookieValue = getCookie(CONFIG_COOKIE_NAME);
                            if (!cookieValue || cookieValue === '') {
                                // Cookie was cleared (reset_config case) - accept server defaults
                                console.log('[Store] Cookie cleared, accepting server default config');
                                saveConfigToStorage(statusConfig);
                                set({
                                    isRunning: message.running,
                                    config: statusConfig,
                                });
                            } else {
                                console.log('[Store] Keeping saved config, ignoring server default');
                                // Still update isRunning from server
                                set({
                                    isRunning: message.running,
                                    // Keep current config (from localStorage)
                                });
                            }
                        }
                    } else {
                        set({
                            isRunning: message.running,
                        });
                    }
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

                case 'error':
                    // Error occurred (panic or OOM) - simulation should be stopped
                    console.error('[Store] Simulation error:', message.error);
                    // Error message will be shown in UI, but also ensure isRunning is false
                    // (server should send status message with running=false, but be defensive)
                    set({ isRunning: false });
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
