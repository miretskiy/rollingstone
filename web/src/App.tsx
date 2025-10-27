import { useEffect } from 'react';
import { useStore } from './store';
import { SimulationControls } from './components/SimulationControls';
import { MetricsDashboard } from './components/MetricsDashboard';
import { LSMTreeVisualization } from './components/LSMTreeVisualization';
import { EventLog } from './components/EventLog';

function App() {
  const { connect, disconnect } = useStore();

  useEffect(() => {
    const wsUrl = `ws://${window.location.hostname}:8080/ws`;
    console.log('Connecting to WebSocket:', wsUrl);
    connect(wsUrl);
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);

  return (
    <div className="min-h-screen bg-dark-bg text-gray-100 p-6">
      <div className="max-w-[2000px] mx-auto space-y-6">
        {/* Header */}
        <header className="text-center mb-8">
          <h1 className="text-5xl font-bold bg-linear-to-r from-primary-400 to-purple-500 bg-clip-text text-transparent pb-2 leading-tight">
            RollingStone
          </h1>
          <p className="text-gray-400 text-sm">
            RocksDB LSM Tree Simulator
          </p>
        </header>

        {/* Simulation Controls */}
        <SimulationControls />

        {/* Metrics Dashboard */}
        <MetricsDashboard />

        {/* LSM Tree Visualization */}
        <LSMTreeVisualization />

        {/* Event Log */}
        <EventLog />

        {/* Footer */}
        <footer className="text-center text-gray-600 text-sm pt-8 pb-4">
          <p>Discrete Event Simulation • Real-time Visualization • Performance Analysis</p>
        </footer>
      </div>
    </div>
  );
}

export default App;
