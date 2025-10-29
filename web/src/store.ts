import { create } from 'zustand';
import type {
    SimulationConfig,
    SimulationMetrics,
    SimulationState,
    SimulationEvent,
    WSMessage,
    ConnectionStatus,
} from './types';

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

export const useStore = create<AppStore>((set, get) => ({
    // Initial state
    connectionStatus: 'disconnected',
    ws: null,
    isRunning: false,
    config: {
        writeRateMBps: 10,
        memtableFlushSizeMB: 64,
        maxWriteBufferNumber: 2,
        l0CompactionTrigger: 4,
        maxBytesForLevelBaseMB: 256,
        levelMultiplier: 10,
        targetFileSizeMB: 64,
        compactionReductionFactor: 0.9,
        maxBackgroundJobs: 2,
        maxSubcompactions: 1,
        maxCompactionBytesMB: 1600,
        ioLatencyMs: 5,
        ioThroughputMBps: 500,
        numLevels: 7,
        initialLSMSizeMB: 0,
        simulationSpeedMultiplier: 1,
    },
    currentMetrics: null,
    metricsHistory: [],
    currentState: null,
    events: [],

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
            currentMetrics: null,
            currentState: null,
        });
    },

    step: () => {
        get().sendMessage({ type: 'step' });
    },

    updateConfig: (configUpdate: Partial<SimulationConfig>) => {
        const newConfig = { ...get().config, ...configUpdate };
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
                    set({
                        isRunning: message.running,
                        config: message.config,
                    });
                    break;

                case 'error':
                    console.error('❌ Server error:', message.error);
                    // Show error to user - you could add a toast/notification here
                    alert(`Configuration error: ${message.error}`);
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
