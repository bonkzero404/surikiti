# Surikiti Examples

This directory contains example configurations, test backends, scripts, and certificates for the Surikiti reverse proxy.

## Directory Structure

```
examples/
├── config/           # Example configuration files
│   ├── api.toml      # API server configuration
│   ├── global.toml   # Global upstream definitions
│   ├── main.toml     # Main HTTP server configuration
│   └── websocket.toml # WebSocket server configuration
├── certs/            # TLS certificates for HTTPS/HTTP2/HTTP3
│   ├── server.crt    # Server certificate
│   └── server.key    # Server private key
├── scripts/          # Utility scripts
│   ├── generate-certs.sh    # Generate self-signed certificates
│   ├── start-backends.sh    # Start all test backends
│   └── test-protocols.sh    # Test different protocols
└── test-backends/    # Test backend servers
    ├── README.md     # Backend documentation
    ├── backend1.py   # HTTP backend server 1
    ├── backend2.py   # HTTP backend server 2
    ├── backend3.py   # HTTP backend server 3
    ├── websocket_backend.py # WebSocket backend server
    ├── test_websocket.py    # WebSocket test client
    └── requirements.txt     # Python dependencies
```

## Quick Start

### 1. Start Test Backends

```bash
# From project root directory
./examples/scripts/start-backends.sh
```

This will start:
- HTTP Backend 1 on port 3001
- HTTP Backend 2 on port 3002
- HTTP Backend 3 on port 3003
- WebSocket Backend on port 3004

### 2. Run Surikiti with Example Configs

```bash
# From project root directory
./surikiti --configs examples/config
```

This will start multiple servers:
- Main HTTP server on port 8086
- API server on port 9086
- WebSocket server on port 9087

### 3. Test the Setup

```bash
# Test HTTP endpoints
curl http://localhost:8086/health
curl http://localhost:9086/api/data

# Test WebSocket (requires wscat)
wscat -c ws://localhost:9087
```

## Configuration Files

### Multi-Server Configuration

The example configurations demonstrate a multi-server setup:

- **main.toml**: Primary HTTP server handling general traffic
- **api.toml**: Dedicated API server for API endpoints
- **websocket.toml**: WebSocket server for real-time communication
- **global.toml**: Shared upstream server definitions

### Key Features Demonstrated

- **Load Balancing**: Round-robin distribution across multiple backends
- **Health Checks**: Automatic monitoring of backend server health
- **CORS Support**: Cross-origin request handling
- **Multi-Protocol**: HTTP/1.1, HTTP/2, HTTP/3, and WebSocket support
- **TLS Configuration**: HTTPS support with provided certificates

## TLS Certificates

The `certs/` directory contains self-signed certificates for testing HTTPS, HTTP/2, and HTTP/3:

- **server.crt**: Server certificate
- **server.key**: Private key

### Generating New Certificates

```bash
# Generate new self-signed certificates
./examples/scripts/generate-certs.sh
```

## Test Backends

The `test-backends/` directory contains Python-based backend servers for testing:

### HTTP Backends
- **backend1.py**: Basic HTTP server with health checks
- **backend2.py**: HTTP server with API endpoints
- **backend3.py**: HTTP server with file operations

### WebSocket Backend
- **websocket_backend.py**: WebSocket server with broadcasting capabilities
- **test_websocket.py**: WebSocket client for testing

### Installation

```bash
# Install Python dependencies
cd examples/test-backends
pip3 install -r requirements.txt
```

See [test-backends/README.md](test-backends/README.md) for detailed documentation.

## Scripts

### Available Scripts

- **start-backends.sh**: Start all test backend servers
- **generate-certs.sh**: Generate self-signed TLS certificates
- **test-protocols.sh**: Test different protocol endpoints

### Usage

```bash
# Make scripts executable
chmod +x examples/scripts/*.sh

# Start backends
./examples/scripts/start-backends.sh

# Generate certificates
./examples/scripts/generate-certs.sh

# Test protocols
./examples/scripts/test-protocols.sh
```

## Testing Different Protocols

### HTTP/1.1
```bash
curl http://localhost:8086/health
```

### HTTP/2 (requires TLS)
```bash
curl --http2 https://localhost:8443/health -k
```

### HTTP/3 (requires TLS and HTTP/3 support)
```bash
curl --http3 https://localhost:8443/health -k
```

### WebSocket
```bash
# Using wscat
wscat -c ws://localhost:9087

# Using websocat
websocat ws://localhost:9087
```

## Customization

### Modifying Configurations

1. Copy example configs to your own directory:
   ```bash
   cp -r examples/config my-config
   ```

2. Modify the configurations as needed

3. Run with your custom configs:
   ```bash
   ./surikiti --configs my-config
   ```

### Adding New Backends

1. Create new backend server (Python, Node.js, etc.)
2. Add upstream configuration to `global.toml`
3. Update server configurations to include new upstream
4. Restart Surikiti

## Troubleshooting

### Common Issues

1. **Port conflicts**: Ensure ports 3001-3004, 8086, 9086, 9087 are available
2. **Certificate errors**: Use `-k` flag with curl for self-signed certificates
3. **Backend not responding**: Check if test backends are running with `lsof -i :3001-3004`
4. **Permission denied**: Make scripts executable with `chmod +x`

### Logs

Check logs for debugging:
```bash
# Surikiti logs
tail -f logs/surikiti_*.log

# Backend logs (if running in background)
ps aux | grep python
```

## Production Considerations

These examples are for development and testing only. For production:

1. **Use proper TLS certificates** from a trusted CA
2. **Secure configurations** with appropriate timeouts and limits
3. **Monitor backend health** with proper health check endpoints
4. **Configure logging** for production environments
5. **Set up monitoring** and alerting
6. **Use environment-specific configs** instead of examples

## Contributing

To add new examples:

1. Create new configuration files in `config/`
2. Add corresponding test backends if needed
3. Update this README with documentation
4. Test the complete setup
5. Submit a pull request

For questions or issues with examples, please check the main [README.md](../README.md) or open an issue on GitHub.