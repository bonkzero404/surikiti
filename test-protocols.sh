#!/bin/bash

# Test script for HTTP/2, HTTP/3, and WebSocket support

echo "🧪 Testing HTTP/2, HTTP/3, and WebSocket support..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to test HTTP/1.1
test_http1() {
    echo -e "\n${BLUE}🌐 Testing HTTP/1.1...${NC}"
    
    response=$(curl -s -w "HTTP_CODE:%{http_code}|TIME:%{time_total}" http://localhost:8090/health 2>/dev/null)
    
    if [[ $? -eq 0 ]]; then
        http_code=$(echo "$response" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)
        time_total=$(echo "$response" | grep -o 'TIME:[0-9.]*' | cut -d: -f2)
        
        if [[ "$http_code" == "200" ]]; then
            echo -e "${GREEN}✅ HTTP/1.1: Success (${http_code}) - ${time_total}s${NC}"
        else
            echo -e "${YELLOW}⚠️  HTTP/1.1: Unexpected status (${http_code})${NC}"
        fi
    else
        echo -e "${RED}❌ HTTP/1.1: Connection failed${NC}"
    fi
}

# Function to test HTTP/2
test_http2() {
    echo -e "\n${BLUE}🚀 Testing HTTP/2...${NC}"
    
    if ! command_exists curl; then
        echo -e "${RED}❌ curl not found${NC}"
        return
    fi
    
    # Check if certificates exist
    if [[ ! -f "certs/server.crt" || ! -f "certs/server.key" ]]; then
        echo -e "${YELLOW}⚠️  TLS certificates not found. Run ./generate-certs.sh first${NC}"
        return
    fi
    
    # Test HTTP/2 with curl
    response=$(curl -s -k --http2 -w "HTTP_CODE:%{http_code}|TIME:%{time_total}|HTTP_VERSION:%{http_version}" https://localhost:8443/health 2>/dev/null)
    
    if [[ $? -eq 0 ]]; then
        http_code=$(echo "$response" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)
        time_total=$(echo "$response" | grep -o 'TIME:[0-9.]*' | cut -d: -f2)
        http_version=$(echo "$response" | grep -o 'HTTP_VERSION:[0-9.]*' | cut -d: -f2)
        
        if [[ "$http_code" == "200" ]]; then
            echo -e "${GREEN}✅ HTTP/2: Success (${http_code}) - HTTP/${http_version} - ${time_total}s${NC}"
        else
            echo -e "${YELLOW}⚠️  HTTP/2: Unexpected status (${http_code})${NC}"
        fi
    else
        echo -e "${RED}❌ HTTP/2: Connection failed (server may not be running on port 8443)${NC}"
    fi
}

# Function to test HTTP/3
test_http3() {
    echo -e "\n${BLUE}🛸 Testing HTTP/3...${NC}"
    
    if ! command_exists curl; then
        echo -e "${RED}❌ curl not found${NC}"
        return
    fi
    
    # Check if curl supports HTTP/3
    if ! curl --version | grep -q "HTTP3"; then
        echo -e "${YELLOW}⚠️  curl doesn't support HTTP/3. Install curl with HTTP/3 support${NC}"
        echo -e "${YELLOW}   macOS: brew install curl-http3${NC}"
        echo -e "${YELLOW}   Linux: Use curl with --http3 flag if available${NC}"
        return
    fi
    
    # Check if certificates exist
    if [[ ! -f "certs/server.crt" || ! -f "certs/server.key" ]]; then
        echo -e "${YELLOW}⚠️  TLS certificates not found. Run ./generate-certs.sh first${NC}"
        return
    fi
    
    # Test HTTP/3 with curl
    response=$(curl -s -k --http3 -w "HTTP_CODE:%{http_code}|TIME:%{time_total}|HTTP_VERSION:%{http_version}" https://localhost:8443/health 2>/dev/null)
    
    if [[ $? -eq 0 ]]; then
        http_code=$(echo "$response" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)
        time_total=$(echo "$response" | grep -o 'TIME:[0-9.]*' | cut -d: -f2)
        http_version=$(echo "$response" | grep -o 'HTTP_VERSION:[0-9.]*' | cut -d: -f2)
        
        if [[ "$http_code" == "200" ]]; then
            echo -e "${GREEN}✅ HTTP/3: Success (${http_code}) - HTTP/${http_version} - ${time_total}s${NC}"
        else
            echo -e "${YELLOW}⚠️  HTTP/3: Unexpected status (${http_code})${NC}"
        fi
    else
        echo -e "${RED}❌ HTTP/3: Connection failed (server may not be running on port 8443)${NC}"
    fi
}

# Function to test WebSocket
test_websocket() {
    echo -e "\n${BLUE}🔌 Testing WebSocket...${NC}"
    
    if command_exists node; then
        # Create a simple WebSocket test with Node.js
        cat > /tmp/ws_test.js << 'EOF'
const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8090/ws');

ws.on('open', function open() {
    console.log('✅ WebSocket: Connection established');
    ws.send('Hello Server!');
});

ws.on('message', function message(data) {
    console.log('📨 WebSocket: Received:', data.toString());
    ws.close();
});

ws.on('error', function error(err) {
    console.log('❌ WebSocket: Error -', err.message);
});

ws.on('close', function close() {
    console.log('🔌 WebSocket: Connection closed');
    process.exit(0);
});

// Timeout after 5 seconds
setTimeout(() => {
    console.log('⏰ WebSocket: Test timeout');
    ws.close();
    process.exit(1);
}, 5000);
EOF
        
        echo -e "${YELLOW}📝 Running WebSocket test with Node.js...${NC}"
        timeout 10s node /tmp/ws_test.js 2>/dev/null
        
        if [[ $? -eq 0 ]]; then
            echo -e "${GREEN}✅ WebSocket test completed${NC}"
        else
            echo -e "${RED}❌ WebSocket test failed or timed out${NC}"
            echo -e "${YELLOW}   Note: WebSocket upgrade is not fully supported in gnet mode${NC}"
        fi
        
        rm -f /tmp/ws_test.js
    else
        echo -e "${YELLOW}⚠️  Node.js not found. Install Node.js to test WebSocket${NC}"
        echo -e "${YELLOW}   Alternative: Use browser developer tools or wscat${NC}"
    fi
}

# Function to show configuration status
show_config_status() {
    echo -e "\n${BLUE}⚙️  Configuration Status:${NC}"
    
    if [[ -f "config.toml" ]]; then
        http2_enabled=$(grep "enable_http2" config.toml | grep -o "true\|false")
        http3_enabled=$(grep "enable_http3" config.toml | grep -o "true\|false")
        websocket_enabled=$(grep "enable_websocket" config.toml | grep -o "true\|false")
        tls_cert=$(grep "tls_cert_file" config.toml | cut -d'"' -f2)
        tls_key=$(grep "tls_key_file" config.toml | cut -d'"' -f2)
        
        echo -e "📋 HTTP/2: ${http2_enabled:-false}"
        echo -e "📋 HTTP/3: ${http3_enabled:-false}"
        echo -e "📋 WebSocket: ${websocket_enabled:-false}"
        echo -e "📋 TLS Cert: ${tls_cert:-not configured}"
        echo -e "📋 TLS Key: ${tls_key:-not configured}"
        
        if [[ -n "$tls_cert" && -f "$tls_cert" ]]; then
            echo -e "${GREEN}✅ TLS certificate file exists${NC}"
        elif [[ -n "$tls_cert" ]]; then
            echo -e "${RED}❌ TLS certificate file not found: $tls_cert${NC}"
        fi
        
        if [[ -n "$tls_key" && -f "$tls_key" ]]; then
            echo -e "${GREEN}✅ TLS key file exists${NC}"
        elif [[ -n "$tls_key" ]]; then
            echo -e "${RED}❌ TLS key file not found: $tls_key${NC}"
        fi
    else
        echo -e "${RED}❌ config.toml not found${NC}"
    fi
}

# Function to check running services
check_services() {
    echo -e "\n${BLUE}🔍 Checking running services...${NC}"
    
    # Check HTTP/1.1 proxy (port 8090)
    if lsof -i :8090 >/dev/null 2>&1; then
        echo -e "${GREEN}✅ HTTP/1.1 Proxy (port 8090): Running${NC}"
    else
        echo -e "${RED}❌ HTTP/1.1 Proxy (port 8090): Not running${NC}"
    fi
    
    # Check HTTPS/HTTP2 (port 8443)
    if lsof -i :8443 >/dev/null 2>&1; then
        echo -e "${GREEN}✅ HTTPS/HTTP2 (port 8443): Running${NC}"
    else
        echo -e "${YELLOW}⚠️  HTTPS/HTTP2 (port 8443): Not running${NC}"
    fi
    
    # Check backend servers
    for port in 3001 3002 3003; do
        if lsof -i :$port >/dev/null 2>&1; then
            echo -e "${GREEN}✅ Backend Server (port $port): Running${NC}"
        else
            echo -e "${RED}❌ Backend Server (port $port): Not running${NC}"
        fi
    done
}

# Main execution
echo -e "${BLUE}🚀 Surikiti Protocol Testing Suite${NC}"
echo -e "${BLUE}===================================${NC}"

show_config_status
check_services

# Run tests
test_http1
test_http2
test_http3
test_websocket

echo -e "\n${BLUE}📊 Test Summary:${NC}"
echo -e "${BLUE}===============${NC}"
echo -e "✅ HTTP/1.1: Basic proxy functionality"
echo -e "🚀 HTTP/2: Requires TLS certificates and port 8443"
echo -e "🛸 HTTP/3: Requires TLS certificates, port 8443, and HTTP/3-enabled curl"
echo -e "🔌 WebSocket: Limited support in gnet mode (upgrade protocol limitation)"

echo -e "\n${YELLOW}💡 Tips:${NC}"
echo -e "• Run ./generate-certs.sh to create TLS certificates"
echo -e "• Update config.toml with certificate paths"
echo -e "• Restart proxy server after configuration changes"
echo -e "• Use browser dev tools to test WebSocket manually"
echo -e "• Install curl with HTTP/3 support for full testing"