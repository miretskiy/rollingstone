import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface Props {
    children: ReactNode;
}

interface State {
    hasError: boolean;
    error: Error | null;
    errorInfo: ErrorInfo | null;
}

export class ErrorBoundary extends Component<Props, State> {
    constructor(props: Props) {
        super(props);
        this.state = { hasError: false, error: null, errorInfo: null };
    }

    static getDerivedStateFromError(error: Error): State {
        return { hasError: true, error, errorInfo: null };
    }

    componentDidCatch(error: Error, errorInfo: ErrorInfo) {
        console.error('ErrorBoundary caught an error:', error, errorInfo);
        this.setState({ error, errorInfo });
    }

    render() {
        if (this.state.hasError) {
            return (
                <div style={{
                    padding: '40px',
                    backgroundColor: '#1a1a2e',
                    color: '#fff',
                    minHeight: '100vh',
                    fontFamily: 'monospace'
                }}>
                    <h1 style={{ color: '#ff6b6b', marginBottom: '20px' }}>⚠️ Application Error</h1>
                    <div style={{
                        backgroundColor: '#0a0a0f',
                        padding: '20px',
                        borderRadius: '8px',
                        marginBottom: '20px'
                    }}>
                        <h2 style={{ color: '#feca57', fontSize: '18px', marginBottom: '10px' }}>Error:</h2>
                        <pre style={{ color: '#ff6b6b', overflow: 'auto' }}>
                            {this.state.error?.toString()}
                        </pre>
                    </div>
                    {this.state.errorInfo && (
                        <div style={{
                            backgroundColor: '#0a0a0f',
                            padding: '20px',
                            borderRadius: '8px'
                        }}>
                            <h2 style={{ color: '#feca57', fontSize: '18px', marginBottom: '10px' }}>Stack Trace:</h2>
                            <pre style={{ color: '#ddd', fontSize: '12px', overflow: 'auto' }}>
                                {this.state.errorInfo.componentStack}
                            </pre>
                        </div>
                    )}
                    <button
                        onClick={() => window.location.reload()}
                        style={{
                            marginTop: '20px',
                            padding: '10px 20px',
                            backgroundColor: '#0ea5e9',
                            color: '#fff',
                            border: 'none',
                            borderRadius: '8px',
                            cursor: 'pointer',
                            fontSize: '16px'
                        }}
                    >
                        Reload Page
                    </button>
                </div>
            );
        }

        return this.props.children;
    }
}

