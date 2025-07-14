# Protocol Support Documentation

Surikiti Reverse Proxy now supports multiple HTTP protocols and WebSocket connections. This document explains how to configure and use these features.

## Supported Protocols

### 1. HTTP/1.1 (Default)
- **Port**: 8090 (configurable)
- **Features**: Standard HTTP/1.1 reverse proxy
- **TLS**: Optional
- **Configuration**: Always enabled

### 2. HTTP/2
- **Port**: 8443 (configurable via `http3_port`)
- **Features**: Multiplexed connections, server push support
- **TLS**: Required
- **Configuration**: `enable_http2 = true`

### 3. HTTP/3 (QUIC)
- **Port**: 8443 (configurable via `http3_port`)
- **Features**: UDP-based, improved performance
- **TLS**: Required
- **Configuration**: `enable_http3 = true`

### 4. WebSocket
- **Port**: 8090 (same as HTTP/1.1)
- **Features**: Real-time bidirectional communication
- **TLS**: Optional
- **Configuration**: `enable_websocket = true`
- **Note**: Limited support in gnet mode due to protocol upgrade limitations

## Configuration

### Basic Configuration (config.toml)

```toml
[proxy]
# Protocol Support
enable_http2 = true
enable_http3 = true
enable_websocket = true

# HTTP/3 Configuration
http3_port = 8443
tls_cert_file = "certs/server.crt"
tls_key_file = "certs/server.key"

# WebSocket Configuration
websocket_timeout = "30s"
websocket_buffer_size = 4096
```

### TLS Certificate Setup

HTTP/2 and HTTP/3 require TLS certificates. For development, you can generate self-signed certificates:

```bash
# Generate self-signed certificates
./generate-certs.sh
```

For production, use certificates from a trusted Certificate Authority (CA).

### Complete Configuration Example

```toml
[server]
host = "0.0.0.0"
port = 8090

[proxy]
# Basic proxy settings
max_body_size = "10MB"
request_timeout = "30s"
response_timeout = "30s"
max_connections = 1000
buffer_size = 8192
enable_compression = true

# Connection pool settings
max_idle_conns_per_host = 10
max_conns_per_host = 50
idle_conn_timeout = "90s"

# Protocol Support
enable_http2 = true
enable_http3 = true
enable_websocket = true

# HTTP/3 Configuration
http3_port = 8443
tls_cert_file = "certs/server.crt"
tls_key_file = "certs/server.key"

# WebSocket Configuration
websocket_timeout = "30s"
websocket_buffer_size = 4096

# Upstream servers
[[upstream]]
name = "backend1"
url = "http://localhost:3001"
weight = 1
health_check_path = "/health"

[[upstream]]
name = "backend2"
url = "http://localhost:3002"
weight = 1
health_check_path = "/health"

[[upstream]]
name = "backend3"
url = "http://localhost:3003"
weight = 1
health_check_path = "/health"

[load_balancer]
method = "least_connections"
health_check_interval = "30s"
health_check_timeout = "5s"
max_retries = 3

[cors]
enable = true
allowed_origins = ["*"]
allowed_methods = ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
allowed_headers = ["*"]
```

## Usage Examples

### HTTP/1.1 Requests
```bash
# Standard HTTP request
curl http://localhost:8090/api/users

# With headers
curl -H "Content-Type: application/json" \
     -d '{"name":"John"}' \
     http://localhost:8090/api/users
```

### HTTP/2 Requests
```bash
# HTTP/2 request (requires TLS)
curl --http2 -k https://localhost:8443/api/users

# Verify HTTP/2 usage
curl --http2 -k -w "HTTP Version: %{http_version}\n" \
     https://localhost:8443/api/users
```

### HTTP/3 Requests
```bash
# HTTP/3 request (requires HTTP/3-enabled curl)
curl --http3 -k https://localhost:8443/api/users

# Verify HTTP/3 usage
curl --http3 -k -w "HTTP Version: %{http_version}\n" \
     https://localhost:8443/api/users
```

### WebSocket Connections

#### JavaScript (Browser)
```javascript
// Connect to WebSocket
const ws = new WebSocket('ws://localhost:8090/ws');

ws.onopen = function(event) {
    console.log('Connected to WebSocket');
    ws.send('Hello Server!');
};

ws.onmessage = function(event) {
    console.log('Received:', event.data);
};

ws.onclose = function(event) {
    console.log('WebSocket closed');
};

ws.onerror = function(error) {
    console.error('WebSocket error:', error);
};
```

#### Node.js
```javascript
const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8090/ws');

ws.on('open', function open() {
    console.log('Connected to WebSocket');
    ws.send('Hello Server!');
});

ws.on('message', function message(data) {
    console.log('Received:', data.toString());
});
```

#### Python
```python
import asyncio
import websockets

async def test_websocket():
    uri = "ws://localhost:8090/ws"
    async with websockets.connect(uri) as websocket:
        await websocket.send("Hello Server!")
        response = await websocket.recv()
        print(f"Received: {response}")

asyncio.run(test_websocket())
```

## Testing

### Automated Testing
Use the provided test script to verify all protocols:

```bash
# Run comprehensive protocol tests
./test-protocols.sh
```

### Manual Testing

#### 1. Generate Certificates
```bash
./generate-certs.sh
```

#### 2. Update Configuration
Ensure your `config.toml` has the correct certificate paths and protocol settings.

#### 3. Start the Proxy
```bash
go run main.go
```

#### 4. Test Each Protocol
```bash
# HTTP/1.1
curl http://localhost:8090/health

# HTTP/2
curl --http2 -k https://localhost:8443/health

# HTTP/3 (if supported)
curl --http3 -k https://localhost:8443/health

# WebSocket (using wscat if available)
wscat -c ws://localhost:8090/ws
```

## Performance Considerations

### HTTP/2 Benefits
- **Multiplexing**: Multiple requests over single connection
- **Header Compression**: Reduced bandwidth usage
- **Server Push**: Proactive resource delivery
- **Binary Protocol**: More efficient parsing

### HTTP/3 Benefits
- **QUIC Protocol**: UDP-based, faster connection establishment
- **Built-in Encryption**: TLS 1.3 by default
- **Connection Migration**: Survives network changes
- **Reduced Head-of-Line Blocking**: Independent stream processing

### WebSocket Benefits
- **Real-time Communication**: Low-latency bidirectional data
- **Persistent Connections**: Reduced connection overhead
- **Custom Protocols**: Application-specific messaging

## Troubleshooting

### Common Issues

#### 1. HTTP/2 Not Working
- **Check TLS certificates**: HTTP/2 requires valid TLS
- **Verify port**: Ensure port 8443 is accessible
- **Client support**: Ensure client supports HTTP/2

#### 2. HTTP/3 Not Working
- **Curl version**: Requires curl with HTTP/3 support
- **Firewall**: UDP port 8443 must be open
- **QUIC support**: Not all clients support HTTP/3

#### 3. WebSocket Issues
- **Protocol limitation**: gnet has limited WebSocket upgrade support
- **Timeout settings**: Adjust `websocket_timeout` if needed
- **Buffer size**: Increase `websocket_buffer_size` for large messages

#### 4. Certificate Issues
- **Self-signed warnings**: Browsers will warn about self-signed certificates
- **Path errors**: Ensure certificate paths in config.toml are correct
- **Permissions**: Certificate files should be readable by the proxy

### Debug Commands

```bash
# Check if ports are listening
lsof -i :8090  # HTTP/1.1
lsof -i :8443  # HTTP/2/3

# Test certificate validity
openssl x509 -in certs/server.crt -text -noout

# Check curl HTTP/3 support
curl --version | grep HTTP3

# Verify configuration
grep -E "enable_http|tls_" config.toml
```

## Security Considerations

### TLS Configuration
- Use strong cipher suites
- Keep certificates up to date
- Use certificates from trusted CAs in production
- Consider certificate pinning for enhanced security

### WebSocket Security
- Validate WebSocket origins
- Implement authentication for WebSocket connections
- Use WSS (WebSocket Secure) in production
- Limit message sizes to prevent DoS attacks

### General Security
- Keep proxy updated
- Monitor for security vulnerabilities
- Use proper firewall rules
- Implement rate limiting

## Future Enhancements

### Planned Features
- **HTTP/2 Server Push**: Proactive resource delivery
- **WebSocket Authentication**: Built-in auth support
- **Protocol Negotiation**: Automatic protocol selection
- **Advanced Load Balancing**: Protocol-aware routing
- **Metrics**: Protocol-specific performance metrics

### Contributing
To contribute protocol improvements:
1. Fork the repository
2. Create a feature branch
3. Implement changes with tests
4. Submit a pull request

For questions or issues, please open a GitHub issue with the "protocol" label.