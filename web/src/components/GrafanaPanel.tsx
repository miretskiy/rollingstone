import { ExternalLink } from 'lucide-react';

export function GrafanaPanel() {
  return (
    <div className="border border-dark-border rounded-lg overflow-hidden bg-dark-card">
      <div className="bg-dark-bg p-3 flex items-center justify-between border-b border-dark-border">
        <h3 className="text-lg font-semibold text-gray-300">Grafana Dashboards</h3>
        <a
          href="http://localhost:3000"
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary-400 hover:text-primary-300 flex items-center gap-1 text-sm"
        >
          Open in new tab <ExternalLink className="w-4 h-4" />
        </a>
      </div>
      <iframe
        src="http://localhost:3000/d/rollingstone/rollingstone-lsm-simulator?orgId=1&refresh=5s&theme=dark&kiosk=tv"
        width="100%"
        height="600px"
        style={{ border: 'none' }}
        title="Grafana Dashboard"
      />
      <div className="bg-dark-bg p-2 text-xs text-gray-500 border-t border-dark-border">
        <strong>First time setup:</strong> Import <code>grafana-dashboard.json</code> at{' '}
        <a
          href="http://localhost:3000/dashboard/import"
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary-400 hover:text-primary-300 underline"
        >
          Grafana Import
        </a>
        {' '}then reload this page.
      </div>
    </div>
  );
}
