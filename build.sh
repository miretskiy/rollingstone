#!/bin/bash
set -e  # Exit on any error

echo "🔨 Building RollingStone..."
echo ""

# Check for required dependencies
echo "🔍 Checking dependencies..."

# Check for Node.js
if ! command -v node &> /dev/null; then
    echo "❌ Node.js is not installed"
    echo "   Install with: brew install node"
    exit 1
fi

# Check for npm
if ! command -v npm &> /dev/null; then
    echo "❌ npm is not installed"
    echo "   Install with: brew install node"
    exit 1
fi

# Check for Go
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed"
    echo "   Install with: brew install go"
    exit 1
fi

# Check Go version
GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
REQUIRED_GO_VERSION="1.21.0"
if [ "$(printf '%s\n' "$REQUIRED_GO_VERSION" "$GO_VERSION" | sort -V | head -n1)" != "$REQUIRED_GO_VERSION" ]; then
    echo "⚠️  Warning: Go version $GO_VERSION found, but $REQUIRED_GO_VERSION or higher is required"
fi

echo "✅ All dependencies found"
echo ""

# Install and build frontend
echo "📦 Installing frontend dependencies..."
cd web
npm install
echo ""

echo "📦 Building frontend..."
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

