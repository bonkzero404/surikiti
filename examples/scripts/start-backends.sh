#!/bin/bash

echo "Starting backend servers for testing..."

# Function to kill background processes on exit
cleanup() {
    echo "\nStopping all backend servers..."
    kill $(jobs -p) 2>/dev/null
    exit
}

# Set trap to cleanup on script exit
trap cleanup SIGINT SIGTERM EXIT

# Start backend servers in background
echo "Starting Backend Server 1 on port 3001..."
python3 test-backends/backend1.py &
BACKEND1_PID=$!

echo "Starting Backend Server 2 on port 3002..."
python3 test-backends/backend2.py &
BACKEND2_PID=$!

echo "Starting Backend Server 3 on port 3003..."
python3 test-backends/backend3.py &
BACKEND3_PID=$!

echo "Starting WebSocket Backend on port 3004..."
python3 test-backends/websocket_backend.py &
WEBSOCKET_PID=$!

# Wait a moment for servers to start
sleep 3

echo "\nAll backend servers started!"
echo "Backend 1: http://localhost:3001"
echo "Backend 2: http://localhost:3002"
echo "Backend 3: http://localhost:3003"
echo "WebSocket Backend: ws://localhost:3004"
echo "\nHealth check endpoints:"
echo "Backend 1: http://localhost:3001/health"
echo "Backend 2: http://localhost:3002/health"
echo "Backend 3: http://localhost:3003/health"
echo "\nWebSocket testing:"
echo "wscat -c ws://localhost:3004"
echo "Send: {\"type\": \"ping\", \"message\": \"hello\"}"
echo "\nPress Ctrl+C to stop all servers"

# Wait for all background processes
wait