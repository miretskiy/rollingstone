#!/bin/bash
set -e  # Exit on any error

echo "🚀 Starting RollingStone..."
echo ""

# Clean up any existing processes on port 8080
echo "🧹 Cleaning up existing processes on port 8080..."
lsof -ti:8080 2>/dev/null | xargs kill -9 2>/dev/null || true
sleep 1
echo "✅ Port 8080 is free"
echo ""

# Build everything
./build.sh
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
    echo "🌐 Server running at: http://localhost:8080"
    echo "📊 Server PID: $SERVER_PID"
    echo "📝 Server logs: tail -f server.log"
    echo "🛑 To stop: kill $SERVER_PID  OR  curl http://localhost:8080/quitquitquit"
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
