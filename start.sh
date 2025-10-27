#!/bin/bash
# RollingStone Unified Startup Script
# Builds frontend if needed, compiles backend, and starts the server

set -e  # Exit on error

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}🚀 Starting RollingStone...${NC}"

# Check if web/dist exists
if [ ! -d "web/dist" ]; then
    echo -e "${YELLOW}📦 Frontend not built. Building...${NC}"
    cd web
    
    # Check if node_modules exists
    if [ ! -d "node_modules" ]; then
        echo -e "${YELLOW}📦 Installing frontend dependencies...${NC}"
        npm install
    fi
    
    echo -e "${YELLOW}🔨 Building frontend...${NC}"
    npm run build
    cd ..
    echo -e "${GREEN}✅ Frontend built successfully${NC}"
else
    echo -e "${GREEN}✅ Frontend already built (web/dist found)${NC}"
fi

# Build Go backend
echo -e "${YELLOW}🔨 Building backend...${NC}"
go build -o /tmp/rollingstone cmd/server/main.go
echo -e "${GREEN}✅ Backend built successfully${NC}"

# Start server
echo -e "${GREEN}🌟 Starting RollingStone server on http://localhost:8080${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop, or visit http://localhost:8080/quitquitquit${NC}"
/tmp/rollingstone

