# Proxy Configuration Guide

## Overview
Surikiti proxy now supports advanced configuration options including request size limits, CORS handling, timeouts, and connection management.

## Proxy Configuration

### Basic Settings
```toml
[proxy]
max_body_size = 10485760        # Maximum request body size (10MB)
request_timeout = "3s"          # Timeout for upstream requests
response_timeout = "5s"         # Timeout for response handling
max_header_size = 8192          # Maximum header size in bytes
keep_alive_timeout = "60s"      # Keep-alive timeout
max_connections = 1000          # Maximum concurrent connections
buffer_size = 4096              # Buffer size for I/O operations
enable_compression = true       # Enable response compression
max_idle_conns = 100            # Maximum idle connections in pool
max_idle_conns_per_host = 10    # Maximum idle connections per host
max_conns_per_host = 50         # Maximum connections per host
idle_conn_timeout = "90s"       # Idle connection timeout
```

### CORS Configuration
```toml
[cors]
enabled = true
allowed_origins = ["*"]
allowed_methods = ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
allowed_headers = ["Content-Type", "Authorization", "X-Requested-With"]
exposed_headers = ["X-Total-Count"]
allow_credentials = false
max_age = 86400
```

## Features

### Request Size Limiting
- Automatically rejects requests exceeding `max_body_size`
- Returns HTTP 413 (Request Entity Too Large) for oversized requests
- Configurable per deployment needs

### CORS Support
- Automatic handling of preflight OPTIONS requests
- Configurable allowed origins, methods, and headers
- Support for credentials and exposed headers
- Caching of preflight responses with `max_age`

### Connection Management
- Configurable timeouts for requests and responses
- Maximum connection limits
- Keep-alive timeout configuration
- Buffer size optimization

### Connection Pooling
- HTTP connection pooling for improved performance
- Configurable pool sizes per host and globally
- Idle connection timeout management
- Connection reuse for reduced latency
- Automatic connection cleanup

### Compression
- Optional response compression
- Reduces bandwidth usage
- Improves performance for text-based responses

## Usage Examples

### Development Setup
```toml
[proxy]
max_body_size = 52428800  # 50MB for file uploads
request_timeout = "10s"

[cors]
enabled = true
allowed_origins = ["http://localhost:3000", "http://localhost:8080"]
allow_credentials = true
```

### Production Setup
```toml
[proxy]
max_body_size = 10485760  # 10MB limit
request_timeout = "3s"
max_connections = 5000

[cors]
enabled = true
allowed_origins = ["https://yourdomain.com"]
allow_credentials = false
```

### API-Only Setup
```toml
[proxy]
max_body_size = 1048576   # 1MB for API requests
request_timeout = "2s"

[cors]
enabled = true
allowed_methods = ["GET", "POST", "PUT", "DELETE"]
allowed_headers = ["Content-Type", "Authorization"]
```

## Security Considerations

1. **Body Size Limits**: Set appropriate limits to prevent DoS attacks
2. **CORS Origins**: Use specific origins in production, avoid "*"
3. **Timeouts**: Configure reasonable timeouts to prevent resource exhaustion
4. **Headers**: Only allow necessary headers in CORS configuration
5. **Credentials**: Only enable when absolutely necessary

## Performance Tips

1. **Buffer Size**: Adjust based on typical request/response sizes
2. **Compression**: Enable for text-heavy APIs
3. **Keep-Alive**: Use longer timeouts for high-traffic scenarios
4. **Connection Limits**: Set based on server capacity
5. **Timeouts**: Balance between user experience and resource usage
6. **Connection Pooling**: 
   - Set `max_idle_conns_per_host` to 10-20 for high-traffic upstreams
   - Use `max_conns_per_host` to limit concurrent connections per backend
   - Configure `idle_conn_timeout` longer than `keep_alive_timeout`
   - Monitor connection pool metrics for optimal tuning
7. **Pool Sizing**: Start with defaults and adjust based on load testing results