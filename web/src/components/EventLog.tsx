import { Activity, Clock } from 'lucide-react';
import { useStore } from '../store';

export function EventLog() {
    const { events } = useStore();

    const formatTime = (seconds: number) => {
        if (seconds < 60) return `${seconds.toFixed(2)}s`;
        if (seconds < 3600) return `${(seconds / 60).toFixed(1)}m`;
        return `${(seconds / 3600).toFixed(1)}h`;
    };

    const getEventColor = (type: string) => {
        switch (type) {
            case 'flush':
                return 'text-green-400';
            case 'compaction':
                return 'text-orange-400';
            case 'read':
                return 'text-blue-400';
            case 'write':
                return 'text-purple-400';
            default:
                return 'text-gray-400';
        }
    };

    const getEventIcon = (type: string) => {
        return <Activity className={`w-3 h-3 ${getEventColor(type)}`} />;
    };

    return (
        <div className="bg-dark-card border border-dark-border rounded-lg shadow-lg">
            <div className="p-4 border-b border-dark-border">
                <div className="flex items-center gap-2">
                    <Clock className="w-5 h-5 text-primary-400" />
                    <h3 className="text-lg font-semibold">Event Log</h3>
                    <span className="text-sm text-gray-500 ml-auto">Last {events.length} events</span>
                </div>
            </div>

            <div className="max-h-96 overflow-y-auto">
                {events.length === 0 ? (
                    <div className="p-8 text-center text-gray-500">
                        No events yet. Start the simulation to see events.
                    </div>
                ) : (
                    <div className="divide-y divide-dark-border">
                        {events.map((event, idx) => (
                            <div
                                key={idx}
                                className="p-3 hover:bg-dark-bg transition-colors"
                            >
                                <div className="flex items-start gap-3">
                                    <div className="mt-1">
                                        {getEventIcon(event.type)}
                                    </div>
                                    <div className="flex-1 min-w-0">
                                        <div className="flex items-baseline gap-2 mb-1">
                                            <span className={`text-sm font-semibold uppercase ${getEventColor(event.type)}`}>
                                                {event.type}
                                            </span>
                                            {event.level !== undefined && (
                                                <span className="text-xs text-gray-500">
                                                    L{event.level}
                                                </span>
                                            )}
                                            <span className="text-xs text-gray-600 ml-auto">
                                                {formatTime(event.timestamp)}
                                            </span>
                                        </div>
                                        <p className="text-sm text-gray-300">
                                            {event.message}
                                        </p>
                                    </div>
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>
        </div>
    );
}

