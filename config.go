package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Servers            []ServerConfig       `mapstructure:"servers"`
	Upstreams          []UpstreamConfig     `mapstructure:"upstreams"`
	WebSocketUpstreams []UpstreamConfig     `mapstructure:"websocket_upstreams"`
	LoadBalancer       LoadBalancerConfig   `mapstructure:"load_balancer"`
	Logging            LoggingConfig        `mapstructure:"logging"`
	Proxy              ProxyConfig          `mapstructure:"proxy"`
	CORS               CORSConfig           `mapstructure:"cors"`
	GlobalDefaults     *GlobalDefaults      `mapstructure:"global_defaults"`
}

// GlobalDefaults contains fallback configurations
type GlobalDefaults struct {
	LoadBalancer LoadBalancerConfig `mapstructure:"load_balancer"`
	Logging      LoggingConfig      `mapstructure:"logging"`
	Proxy        ProxyConfig        `mapstructure:"proxy"`
	CORS         CORSConfig         `mapstructure:"cors"`
}

// ServerFileConfig represents a single server configuration file
type ServerFileConfig struct {
	Server       ServerConfig       `mapstructure:"server"`
	LoadBalancer LoadBalancerConfig `mapstructure:"load_balancer"`
	Logging      LoggingConfig      `mapstructure:"logging"`
	Proxy        ProxyConfig        `mapstructure:"proxy"`
	CORS         CORSConfig         `mapstructure:"cors"`
}

type ServerConfig struct {
	Name          string              `mapstructure:"name"`
	Port          int                 `mapstructure:"port"`
	Host          string              `mapstructure:"host"`
	WebSocketPort int                 `mapstructure:"websocket_port"`
	Upstreams     []string            `mapstructure:"upstreams"`
	Enabled       bool                `mapstructure:"enabled"`
	// Per-server configurations (optional, falls back to global if not set)
	LoadBalancer  *LoadBalancerConfig `mapstructure:"load_balancer,omitempty"`
	Logging       *LoggingConfig      `mapstructure:"logging,omitempty"`
	Proxy         *ProxyConfig        `mapstructure:"proxy,omitempty"`
	CORS          *CORSConfig         `mapstructure:"cors,omitempty"`
}

type UpstreamConfig struct {
	Name        string `mapstructure:"name"`
	URL         string `mapstructure:"url"`
	Weight      int    `mapstructure:"weight"`
	HealthCheck string `mapstructure:"health_check"`
}

type LoadBalancerConfig struct {
	Method     string        `mapstructure:"method"`
	Timeout    time.Duration `mapstructure:"timeout"`
	MaxRetries int           `mapstructure:"max_retries"`
}

type LoggingConfig struct {
	Level string `mapstructure:"level"`
	File  string `mapstructure:"file"`
}

type ProxyConfig struct {
	MaxBodySize         int64         `mapstructure:"max_body_size"`          // Maximum request body size in bytes
	RequestTimeout      time.Duration `mapstructure:"request_timeout"`       // Request timeout
	ResponseTimeout     time.Duration `mapstructure:"response_timeout"`      // Response timeout
	MaxHeaderSize       int           `mapstructure:"max_header_size"`       // Maximum header size in bytes
	KeepAliveTimeout    time.Duration `mapstructure:"keep_alive_timeout"`    // Keep-alive timeout
	MaxConnections      int           `mapstructure:"max_connections"`       // Maximum concurrent connections
	BufferSize          int           `mapstructure:"buffer_size"`           // Buffer size for reading/writing
	EnableCompression   bool          `mapstructure:"enable_compression"`    // Enable gzip compression
	MaxIdleConns        int           `mapstructure:"max_idle_conns"`        // Maximum idle connections in pool
	MaxIdleConnsPerHost int           `mapstructure:"max_idle_conns_per_host"` // Maximum idle connections per host
	MaxConnsPerHost     int           `mapstructure:"max_conns_per_host"`    // Maximum connections per host
	IdleConnTimeout     time.Duration `mapstructure:"idle_conn_timeout"`     // Idle connection timeout
	// Protocol support
	EnableHTTP2         bool          `mapstructure:"enable_http2"`          // Enable HTTP/2 support
	EnableHTTP3         bool          `mapstructure:"enable_http3"`          // Enable HTTP/3 support
	EnableWebSocket     bool          `mapstructure:"enable_websocket"`      // Enable WebSocket support
	HTTP3Port           int           `mapstructure:"http3_port"`            // HTTP/3 UDP port
	TLSCertFile         string        `mapstructure:"tls_cert_file"`         // TLS certificate file for HTTPS/HTTP2/HTTP3
	TLSKeyFile          string        `mapstructure:"tls_key_file"`          // TLS private key file
	WebSocketTimeout    time.Duration `mapstructure:"websocket_timeout"`     // WebSocket connection timeout
	WebSocketBufferSize int           `mapstructure:"websocket_buffer_size"` // WebSocket buffer size
}

type CORSConfig struct {
	Enabled          bool     `mapstructure:"enabled"`            // Enable CORS
	AllowedOrigins   []string `mapstructure:"allowed_origins"`    // Allowed origins
	AllowedMethods   []string `mapstructure:"allowed_methods"`    // Allowed HTTP methods
	AllowedHeaders   []string `mapstructure:"allowed_headers"`    // Allowed headers
	ExposedHeaders   []string `mapstructure:"exposed_headers"`    // Exposed headers
	AllowCredentials bool     `mapstructure:"allow_credentials"`  // Allow credentials
	MaxAge           int      `mapstructure:"max_age"`            // Preflight cache duration in seconds
}

func LoadConfig(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("toml")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

// LoadMultiFileConfig loads configuration from multiple files
// configDir should contain: global.toml and any number of server .toml files
func LoadMultiFileConfig(configDir string) (*Config, error) {
	// Load global configuration first
	globalPath := filepath.Join(configDir, "global.toml")
	globalViper := viper.New()
	globalViper.SetConfigFile(globalPath)
	globalViper.SetConfigType("toml")

	if err := globalViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read global config file: %w", err)
	}

	var config Config
	if err := globalViper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global config: %w", err)
	}

	// Scan directory for all .toml files (except global.toml)
	serverFiles, err := scanConfigDirectory(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to scan config directory: %w", err)
	}

	// Load individual server configurations
	for _, serverFile := range serverFiles {
		serverPath := filepath.Join(configDir, serverFile)
		serverViper := viper.New()
		serverViper.SetConfigFile(serverPath)
		serverViper.SetConfigType("toml")

		if err := serverViper.ReadInConfig(); err != nil {
			// Skip if file doesn't exist or can't be read
			continue
		}

		var serverConfig ServerFileConfig
		if err := serverViper.Unmarshal(&serverConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal server config %s: %w", serverFile, err)
		}

		// Only add server if it's enabled
		if !serverConfig.Server.Enabled {
			continue
		}

		// Set per-server configurations
		serverConfig.Server.LoadBalancer = &serverConfig.LoadBalancer
		serverConfig.Server.Logging = &serverConfig.Logging
		serverConfig.Server.Proxy = &serverConfig.Proxy
		serverConfig.Server.CORS = &serverConfig.CORS

		// Add server to config
		config.Servers = append(config.Servers, serverConfig.Server)
	}

	// Use global defaults as fallback if they exist
	if config.GlobalDefaults != nil {
		config.LoadBalancer = config.GlobalDefaults.LoadBalancer
		config.Logging = config.GlobalDefaults.Logging
		config.Proxy = config.GlobalDefaults.Proxy
		config.CORS = config.GlobalDefaults.CORS
	}

	return &config, nil
}

// scanConfigDirectory scans the config directory for all .toml files except global.toml
func scanConfigDirectory(configDir string) ([]string, error) {
	var serverFiles []string

	err := filepath.WalkDir(configDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process .toml files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".toml") {
			return nil
		}

		// Skip global.toml as it's handled separately
		if d.Name() == "global.toml" {
			return nil
		}

		// Add relative path from configDir
		relPath, err := filepath.Rel(configDir, path)
		if err != nil {
			return err
		}

		serverFiles = append(serverFiles, relPath)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return serverFiles, nil
}

func (c *Config) GetServerAddress(serverName string) string {
	for _, server := range c.Servers {
		if server.Name == serverName {
			return fmt.Sprintf("%s:%d", server.Host, server.Port)
		}
	}
	return ""
}

// GetEnabledServers returns only enabled servers
func (c *Config) GetEnabledServers() []ServerConfig {
	var enabled []ServerConfig
	for _, server := range c.Servers {
		if server.Enabled {
			enabled = append(enabled, server)
		}
	}
	return enabled
}

// GetUpstreamsByNames returns upstreams filtered by names
func (c *Config) GetUpstreamsByNames(names []string) []UpstreamConfig {
	var filtered []UpstreamConfig
	nameMap := make(map[string]bool)
	for _, name := range names {
		nameMap[name] = true
	}
	
	for _, upstream := range c.Upstreams {
		if nameMap[upstream.Name] {
			filtered = append(filtered, upstream)
		}
	}
	return filtered
}

// GetWebSocketUpstreamsByNames returns websocket upstreams filtered by names
func (c *Config) GetWebSocketUpstreamsByNames(names []string) []UpstreamConfig {
	var filtered []UpstreamConfig
	nameMap := make(map[string]bool)
	for _, name := range names {
		nameMap[name] = true
	}
	
	for _, upstream := range c.WebSocketUpstreams {
		if nameMap[upstream.Name] {
			filtered = append(filtered, upstream)
		}
	}
	return filtered
}

// GetLoadBalancerConfig returns load balancer config for a server (per-server or global)
func (c *Config) GetLoadBalancerConfig(serverName string) LoadBalancerConfig {
	for _, server := range c.Servers {
		if server.Name == serverName && server.LoadBalancer != nil {
			return *server.LoadBalancer
		}
	}
	return c.LoadBalancer
}

// GetLoggingConfig returns logging config for a server (per-server or global)
func (c *Config) GetLoggingConfig(serverName string) LoggingConfig {
	for _, server := range c.Servers {
		if server.Name == serverName && server.Logging != nil {
			return *server.Logging
		}
	}
	return c.Logging
}

// GetProxyConfig returns proxy config for a server (per-server or global)
func (c *Config) GetProxyConfig(serverName string) ProxyConfig {
	for _, server := range c.Servers {
		if server.Name == serverName && server.Proxy != nil {
			return *server.Proxy
		}
	}
	return c.Proxy
}

// GetCORSConfig returns CORS config for a server (per-server or global)
func (c *Config) GetCORSConfig(serverName string) CORSConfig {
	for _, server := range c.Servers {
		if server.Name == serverName && server.CORS != nil {
			return *server.CORS
		}
	}
	return c.CORS
}