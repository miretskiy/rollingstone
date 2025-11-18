import { useState, useEffect } from 'react';
import { HelpCircle } from 'lucide-react';
import { useStore } from '../store';
import type { SimulationConfig } from '../types';

interface ConfigInputProps {
    label: string;
    field: keyof SimulationConfig;
    min: number;
    max: number;
    tooltip?: string;
    unit?: string;
    disabled?: boolean; // External disabled state (e.g., when parent feature is disabled)
}

export function ConfigInput({
    label,
    field,
    min,
    max,
    tooltip,
    unit = '',
    disabled: externalDisabled = false
}: ConfigInputProps) {
    // Use selectors to only re-render when this specific field changes
    const value = useStore(state => (state.config[field] as number));
    const isRunning = useStore(state => state.isRunning);
    const isConnected = useStore(state => state.connectionStatus === 'connected');
    const updateConfig = useStore(state => state.updateConfig);

    // Static params can't be changed while running
    const isStaticParam = field !== 'writeRateMBps' && field !== 'simulationSpeedMultiplier';
    const disabled = externalDisabled || !isConnected || (isRunning && isStaticParam);
    const inputId = `config-${field}`;
    const [localValue, setLocalValue] = useState(String(value));
    const [isFocused, setIsFocused] = useState(false);

    // Only sync when not focused
    useEffect(() => {
        if (!isFocused) {
            setLocalValue(String(value));
        }
    }, [value, isFocused]);

    const displayLabel = unit ? `${label} (${unit})` : label;

    const applyValue = () => {
        const num = parseFloat(localValue);
        if (!isNaN(num)) {
            const clamped = Math.max(min, Math.min(max, num));
            updateConfig({ [field]: clamped });
            setLocalValue(String(clamped));
        } else {
            setLocalValue(String(value));
        }
    };

    return (
        <div className="flex items-center justify-between gap-2">
            <label
                htmlFor={inputId}
                className="text-sm text-gray-300 flex items-center gap-1 flex-1 min-w-0 cursor-pointer"
            >
                {displayLabel}
                {tooltip && (
                    <div className="group relative">
                        <HelpCircle className="w-3 h-3 text-gray-500 cursor-help" tabIndex={-1} />
                        <div className="absolute left-0 bottom-full mb-2 hidden group-hover:block z-50 w-64 p-2 bg-gray-900 border border-gray-700 rounded text-xs text-gray-300 shadow-lg">
                            {tooltip}
                        </div>
                    </div>
                )}
            </label>
            <input
                key={field}
                id={inputId}
                type="text"
                inputMode="numeric"
                value={localValue}
                onChange={(e) => setLocalValue(e.target.value)}
                onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                        applyValue();
                        e.currentTarget.blur();
                    }
                }}
                onFocus={(e) => {
                    console.log(`[${field}] FOCUS`);
                    e.target.select();
                    setIsFocused(true);
                }}
                onBlur={() => {
                    console.log(`[${field}] BLUR`);
                    setIsFocused(false);
                    applyValue();
                }}
                disabled={disabled}
                className="w-20 px-2 py-1 bg-dark-bg border border-dark-border rounded text-right text-xs disabled:opacity-50 disabled:cursor-not-allowed focus:ring-2 focus:ring-primary-500 focus:border-transparent"
            />
        </div>
    );
}

