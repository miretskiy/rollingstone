#!/bin/bash
set -e  # Exit on any error

echo "🚀 Starting RollingStone..."
echo ""

# Clean up any existing processes
echo "🧹 Cleaning up existing processes..."
lsof -ti:8080 2>/dev/null | xargs kill -9 2>/dev/null || true
pkill -f "prometheus.*rollingstone" 2>/dev/null || true
pkill -f "grafana.*server" 2>/dev/null || true
sleep 1
echo "✅ Ports cleaned"
echo ""

# Build everything
./build.sh
echo ""

# Start Prometheus (if installed)
if command -v prometheus &> /dev/null; then
    echo "📊 Starting Prometheus..."
    mkdir -p prom-data
    prometheus --config.file=prometheus.yml --storage.tsdb.path=./prom-data --web.listen-address=:9090 > prometheus.log 2>&1 &
    PROM_PID=$!
    echo "✅ Prometheus started (PID: $PROM_PID, http://localhost:9090)"
else
    echo "⚠️  Prometheus not installed (brew install prometheus for metrics)"
    PROM_PID=""
fi

# Start Grafana (if installed)
if command -v grafana-server &> /dev/null; then
    echo "📈 Starting Grafana..."
    mkdir -p grafana-data
    grafana-server --config=grafana.ini --homepath=/opt/homebrew/opt/grafana/share/grafana > grafana.log 2>&1 &
    GRAF_PID=$!
    echo "✅ Grafana started (PID: $GRAF_PID, http://localhost:3000)"
else
    echo "⚠️  Grafana not installed (brew install grafana for dashboards)"
    GRAF_PID=""
fi
echo ""

# Start server in background
echo "🌟 Starting server in background..."
./rollingstone > server.log 2>&1 &
SERVER_PID=$!

# Wait for server to be ready (max 10 seconds)
echo "⏳ Waiting for server to start..."
MAX_WAIT=10
WAITED=0
while [ $WAITED -lt $MAX_WAIT ]; do
  if curl -s http://localhost:8080 | grep -q "RollingStone" 2>/dev/null; then
    echo "✅ Server started successfully!"
    echo ""
    echo "🌐 Simulator UI: http://localhost:8080"
    echo "📊 Prometheus metrics: http://localhost:8080/metrics"
    if [ -n "$PROM_PID" ]; then
      echo "📈 Prometheus UI: http://localhost:9090"
    fi
    if [ -n "$GRAF_PID" ]; then
      echo "📊 Grafana dashboards: http://localhost:3000 (admin/admin)"
    fi
    echo ""
    echo "📝 Logs: tail -f server.log prometheus.log grafana.log"
    echo "🛑 To stop: curl http://localhost:8080/quitquitquit"
    [ -n "$PROM_PID" ] && echo "           kill $PROM_PID (Prometheus)"
    [ -n "$GRAF_PID" ] && echo "           kill $GRAF_PID (Grafana)"
    echo ""
    exit 0
  fi
  sleep 0.5
  WAITED=$((WAITED + 1))
done

# Server failed to start
echo "❌ Server failed to start within ${MAX_WAIT} seconds"
echo "📝 Check server.log for errors:"
tail -20 server.log
kill $SERVER_PID 2>/dev/null || true
exit 1
