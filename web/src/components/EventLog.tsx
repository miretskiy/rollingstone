import { Clock } from 'lucide-react';
import { useStore } from '../store';

export function EventLog() {
    const logs = useStore(state => state.logs);

    return (
        <div className="bg-dark-card border border-dark-border rounded-lg shadow-lg">
            <div className="p-4 border-b border-dark-border">
                <div className="flex items-center gap-2">
                    <Clock className="w-5 h-5 text-primary-400" />
                    <h3 className="text-lg font-semibold">Event Log</h3>
                    <span className="text-sm text-gray-500 ml-auto">{logs.length} messages</span>
                </div>
            </div>

            <div className="max-h-96 overflow-y-auto font-mono text-xs">
                {logs.length === 0 ? (
                    <div className="p-8 text-center text-gray-500">
                        No events yet. Start the simulation to see events.
                    </div>
                ) : (
                    <div className="divide-y divide-dark-border">
                        {logs.map((logMsg: string, idx: number) => (
                            <div
                                key={idx}
                                className="p-2 hover:bg-dark-bg transition-colors"
                            >
                                <p className="text-gray-300 whitespace-pre-wrap break-words">{logMsg}</p>
                            </div>
                        ))}
                    </div>
                )}
            </div>
        </div>
    );
}

