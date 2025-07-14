package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig       `mapstructure:"server"`
	Upstreams    []UpstreamConfig   `mapstructure:"upstreams"`
	LoadBalancer LoadBalancerConfig `mapstructure:"load_balancer"`
	Logging      LoggingConfig      `mapstructure:"logging"`
	Proxy        ProxyConfig        `mapstructure:"proxy"`
	CORS         CORSConfig         `mapstructure:"cors"`
}

type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Host string `mapstructure:"host"`
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

func (c *Config) GetServerAddress() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}