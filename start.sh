#!/bin/bash
# RollingStone Unified Startup Script
# Builds frontend and backend, then starts the server
#
# Usage:
#   ./start.sh              # Build frontend and backend (default)
#   ./start.sh --skip-ui    # Skip frontend rebuild (faster for backend-only changes)

set -e  # Exit on error

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Parse arguments
SKIP_UI=false
if [ "$1" == "--skip-ui" ]; then
    SKIP_UI=true
fi

echo -e "${GREEN}🚀 Starting RollingStone...${NC}"

# Kill any existing server instances
echo -e "${YELLOW}🔍 Checking for existing server...${NC}"
EXISTING_PID=$(lsof -ti:8080 2>/dev/null || true)
if [ -n "$EXISTING_PID" ]; then
    echo -e "${YELLOW}🛑 Killing existing server (PID: $EXISTING_PID)...${NC}"
    kill -9 $EXISTING_PID 2>/dev/null || true
    sleep 1
    echo -e "${GREEN}✅ Existing server stopped${NC}"
else
    echo -e "${GREEN}✅ No existing server found${NC}"
fi

# Build or check frontend
if [ "$SKIP_UI" = true ]; then
    if [ ! -d "web/dist" ]; then
        echo -e "${RED}❌ Frontend not built and --skip-ui specified. Run without --skip-ui first.${NC}"
        exit 1
    fi
    echo -e "${YELLOW}⏭️  Skipping frontend rebuild (using existing web/dist)${NC}"
else
    echo -e "${YELLOW}🔨 Building frontend...${NC}"
    cd web
    
    # Check if node_modules exists
    if [ ! -d "node_modules" ]; then
        echo -e "${YELLOW}📦 Installing frontend dependencies...${NC}"
        npm install
    fi
    
    npm run build
    cd ..
    echo -e "${GREEN}✅ Frontend built successfully${NC}"
fi

# Build Go backend
echo -e "${YELLOW}🔨 Building backend...${NC}"
go build -o /tmp/rollingstone cmd/server/main.go
echo -e "${GREEN}✅ Backend built successfully${NC}"

# Start server
echo -e "${GREEN}🌟 Starting RollingStone server on http://localhost:8080${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop, or visit http://localhost:8080/quitquitquit${NC}"
/tmp/rollingstone

