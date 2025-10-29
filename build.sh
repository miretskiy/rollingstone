#!/bin/bash
set -e  # Exit on any error

echo "🔨 Building RollingStone..."
echo ""

# Build frontend
echo "📦 Building frontend (npm)..."
cd web
npm run build
cd ..
echo "✅ Frontend built"
echo ""

# Build backend
echo "🔧 Building backend (Go)..."
go build -o rollingstone ./cmd/server
echo "✅ Backend built"
echo ""

echo "🎉 Build complete! Run ./start.sh to start the server"

