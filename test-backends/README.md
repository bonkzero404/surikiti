# Test Backends for Surikiti Reverse Proxy

This directory contains various backend servers for testing different protocols and features of the Surikiti reverse proxy.

## Available Backends

### HTTP Backends

#### Backend 1 (Port 3001)
- **File**: `backend1.py`
- **URL**: `http://localhost:3001`
- **Features**: Basic HTTP server with health checks, request logging
- **Endpoints**:
  - `GET /` - Welcome message
  - `GET /health` - Health check
  - `GET /info` - Server information
  - `POST /echo` - Echo request body

#### Backend 2 (Port 3002)
- **File**: `backend2.py`
- **URL**: `http://localhost:3002`
- **Features**: HTTP server with additional endpoints
- **Endpoints**:
  - `GET /` - Welcome message
  - `GET /health` - Health check
  - `GET /api/data` - Sample JSON data
  - `POST /api/submit` - Data submission endpoint

#### Backend 3 (Port 3003)
- **File**: `backend3.py`
- **URL**: `http://localhost:3003`
- **Features**: HTTP server with file operations
- **Endpoints**:
  - `GET /` - Welcome message
  - `GET /health` - Health check
  - `GET /files` - List files
  - `POST /upload` - File upload endpoint

### WebSocket Backend

#### WebSocket Backend (Port 3004)
- **File**: `websocket_backend.py`
- **URL**: `ws://localhost:3004`
- **Features**: Real-time bidirectional communication, broadcasting, client management
- **Message Types**:
  - `ping` - Ping/pong for connectivity testing
  - `echo` - Echo messages back to sender
  - `broadcast` - Broadcast messages to all connected clients
  - `stats` - Get server statistics

## Installation

### Prerequisites

```bash
# Install Python dependencies
pip3 install -r requirements.txt

# For WebSocket testing, install wscat (optional)
npm install -g wscat
```

### Dependencies

- **Python 3.7+**
- **websockets** (for WebSocket backend)
- **wscat** (optional, for WebSocket testing)

## Usage

### Starting All Backends

Use the provided script to start all backends at once:

```bash
# From project root directory
./scripts/start-backends.sh
```

This will start:
- Backend 1 on port 3001
- Backend 2 on port 3002
- Backend 3 on port 3003
- WebSocket Backend on port 3004

### Starting Individual Backends

```bash
# HTTP Backends
python3 test-backends/backend1.py
python3 test-backends/backend2.py
python3 test-backends/backend3.py

# WebSocket Backend
python3 test-backends/websocket_backend.py

# With custom host/port
python3 test-backends/websocket_backend.py --host 0.0.0.0 --port 3005
```

## Testing

### HTTP Backend Testing

```bash
# Test HTTP backends
curl http://localhost:3001/health
curl http://localhost:3002/api/data
curl -X POST http://localhost:3003/upload -d "test data"

# Through proxy (assuming proxy is running on port 8080)
curl http://localhost:8080/health
```

### WebSocket Backend Testing

#### Using wscat

```bash
# Connect to WebSocket backend
wscat -c ws://localhost:3004

# Send test messages
{"type": "ping", "message": "hello"}
{"type": "echo", "message": "test echo"}
{"type": "broadcast", "message": "hello everyone"}
{"type": "stats"}
```

#### Using curl (WebSocket upgrade)

```bash
# Test WebSocket upgrade through proxy
curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" -H "Sec-WebSocket-Key: test" -H "Sec-WebSocket-Version: 13" http://localhost:8080/
```

#### Programmatic Testing

```python
import asyncio
import websockets
import json

async def test_websocket():
    uri = "ws://localhost:3004"
    async with websockets.connect(uri) as websocket:
        # Send ping
        ping_msg = {"type": "ping", "message": "test"}
        await websocket.send(json.dumps(ping_msg))
        response = await websocket.recv()
        print(f"Ping response: {response}")
        
        # Send echo
        echo_msg = {"type": "echo", "message": "hello world"}
        await websocket.send(json.dumps(echo_msg))
        response = await websocket.recv()
        print(f"Echo response: {response}")
        
        # Get stats
        stats_msg = {"type": "stats"}
        await websocket.send(json.dumps(stats_msg))
        response = await websocket.recv()
        print(f"Stats response: {response}")

# Run the test
asyncio.run(test_websocket())
```

## WebSocket Message Format

### Request Messages

```json
{
  "type": "ping|echo|broadcast|stats",
  "message": "your message content"
}
```

### Response Messages

#### Welcome Message
```json
{
  "type": "welcome",
  "message": "Connected to WebSocket Backend",
  "server_info": {
    "host": "localhost",
    "port": 3004,
    "uptime": 123.45,
    "total_clients": 1
  },
  "timestamp": "2024-01-01T12:00:00.000000"
}
```

#### Pong Response
```json
{
  "type": "pong",
  "original_message": {...},
  "server_time": "2024-01-01T12:00:00.000000",
  "message_count": 1
}
```

#### Echo Response
```json
{
  "type": "echo_response",
  "original_message": "your message",
  "echoed_at": "2024-01-01T12:00:00.000000",
  "message_count": 2
}
```

#### Stats Response
```json
{
  "type": "stats_response",
  "stats": {
    "connected_clients": 3,
    "total_messages": 15,
    "uptime_seconds": 300.5,
    "server_host": "localhost",
    "server_port": 3004
  },
  "timestamp": "2024-01-01T12:00:00.000000"
}
```

## Features

### WebSocket Backend Features

- **Real-time Communication**: Bidirectional messaging between clients and server
- **Client Management**: Automatic registration and cleanup of client connections
- **Broadcasting**: Send messages to all connected clients
- **Health Monitoring**: Periodic heartbeat messages and connection monitoring
- **Message Types**: Support for ping/pong, echo, broadcast, and stats
- **Error Handling**: Graceful error handling and informative error messages
- **Logging**: Comprehensive logging of connections and messages
- **Statistics**: Real-time server and connection statistics

### HTTP Backend Features

- **Health Checks**: Standard `/health` endpoints for load balancer health checks
- **Request Logging**: Detailed logging of all incoming requests
- **JSON Responses**: Structured JSON responses for API testing
- **Error Handling**: Proper HTTP status codes and error responses
- **CORS Support**: Cross-origin resource sharing for web applications

## Configuration

### WebSocket Backend Options

```bash
python3 websocket_backend.py --help

usage: websocket_backend.py [-h] [--host HOST] [--port PORT] [--debug]

WebSocket Backend Server

optional arguments:
  -h, --help   show this help message and exit
  --host HOST  Host to bind to
  --port PORT  Port to bind to
  --debug      Enable debug logging
```

### Example Configurations

```bash
# Bind to all interfaces
python3 websocket_backend.py --host 0.0.0.0

# Use different port
python3 websocket_backend.py --port 3005

# Enable debug logging
python3 websocket_backend.py --debug
```

## Troubleshooting

### Common Issues

1. **Port Already in Use**
   ```bash
   # Check what's using the port
   lsof -i :3004
   
   # Kill the process
   kill -9 <PID>
   ```

2. **WebSocket Connection Failed**
   - Ensure the backend is running
   - Check firewall settings
   - Verify the correct port and host

3. **Missing Dependencies**
   ```bash
   # Install missing dependencies
   pip3 install -r requirements.txt
   ```

4. **Permission Denied**
   ```bash
   # Make scripts executable
   chmod +x scripts/start-backends.sh
   ```

### Logs

All backends provide detailed logging. For WebSocket backend:

```bash
# Enable debug logging
python3 websocket_backend.py --debug
```

## Integration with Surikiti Proxy

These backends are designed to work with the Surikiti reverse proxy. Example proxy configuration:

```toml
[server]
http_port = 8080
websocket_port = 8081

[[upstream]]
name = "http_backends"
protocol = "http"
backends = [
    { address = "127.0.0.1:3001", weight = 1 },
    { address = "127.0.0.1:3002", weight = 1 },
    { address = "127.0.0.1:3003", weight = 1 }
]

[[upstream]]
name = "websocket_backend"
protocol = "websocket"
backends = [
    { address = "127.0.0.1:3004", weight = 1 }
]
```

This allows testing of:
- HTTP load balancing across multiple backends
- WebSocket proxying and connection management
- Health checks and failover
- Protocol-specific routing