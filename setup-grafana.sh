#!/bin/bash
set -e

echo "🔧 Setting up Grafana..."

# Wait for Grafana to be ready
echo "⏳ Waiting for Grafana..."
sleep 3

# Add Prometheus data source
echo "📊 Adding Prometheus data source..."
curl -s -u admin:rollingstone -X POST -H "Content-Type: application/json" -d '{
  "name": "Prometheus",
  "type": "prometheus",
  "url": "http://localhost:9090",
  "access": "proxy",
  "isDefault": true
}' http://localhost:3000/api/datasources

# Import dashboard
echo ""
echo "📈 Importing dashboard..."
curl -s -u admin:rollingstone -X POST -H "Content-Type: application/json" -d @grafana-dashboard.json http://localhost:3000/api/dashboards/db

echo ""
echo "✅ Grafana configured!"
echo "🌐 Dashboard: http://localhost:3000/d/rollingstone"
