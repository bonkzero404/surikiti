/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>

*/
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"surikiti/config"
	"surikiti/loadbalancer"
	"surikiti/proxy"
)

var (
	configsDir  string
	configFile  string
)

// ServerInstance represents a single server instance with its own configuration and load balancers
type ServerInstance struct {
	name            string
	config          config.ServerConfig
	loadBalancer    *loadbalancer.LoadBalancer
	wsLoadBalancer  *loadbalancer.LoadBalancer
	proxyServer     *proxy.ProxyServer
	httpServer      *http.Server
	websocketServer *http.Server
	gnetStarted     chan struct{}
	logger          *zap.Logger
}

// MultiServerManager manages multiple server instances
type MultiServerManager struct {
	serverInstances []*ServerInstance
	shutdownChan    chan struct{}
	mu              sync.RWMutex
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "surikiti",
	Short: "Surikiti Reverse Proxy Server",
	Long: `A high-performance reverse proxy server with load balancing, WebSocket support, and multi-server configuration.

Supports HTTP/1.1, HTTP/2, HTTP/3, and WebSocket protocols with advanced load balancing algorithms.`,
	RunE: runServer,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Add flags
	rootCmd.Flags().StringVar(&configsDir, "configs", "./config", "Path to configuration directory containing TOML files")
	rootCmd.Flags().StringVar(&configFile, "config", "", "Path to single configuration file (legacy mode)")
}

func runServer(cmd *cobra.Command, args []string) error {
	// Load configuration
	var cfg *config.Config
	var err error
	
	if configFile != "" {
		// Legacy mode: single config file
		cfg, err = config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	} else {
		// New mode: multiple config files from directory
		cfg, err = config.LoadMultiFileConfig(configsDir)
		if err != nil {
			return fmt.Errorf("failed to load multi-file config: %w", err)
		}
	}

	// Setup global logger (fallback)
	logger, err := setupLogger(cfg.Logging, "global")
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer logger.Sync()

	// Create context for graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if configFile != "" {
		logger.Info("Starting Surikiti Reverse Proxy",
			zap.String("version", "1.0.0"),
			zap.String("config_mode", "single_file"),
			zap.String("config_path", configFile))
	} else {
		logger.Info("Starting Surikiti Reverse Proxy",
			zap.String("version", "1.0.0"),
			zap.String("config_mode", "multi_file"),
			zap.String("config_dir", configsDir))
	}

	// Get enabled servers
	enabledServers := cfg.GetEnabledServers()
	if len(enabledServers) == 0 {
		return fmt.Errorf("no enabled servers found in configuration")
	}

	logger.Info("Found enabled servers", zap.Int("count", len(enabledServers)))

	// Create multi-server manager
	multiManager := &MultiServerManager{
		shutdownChan: make(chan struct{}),
	}

	// Create server instances
	for _, serverCfg := range enabledServers {
		instance, err := createServerInstance(serverCfg, cfg, logger)
		if err != nil {
			return fmt.Errorf("failed to create server instance %s: %w", serverCfg.Name, err)
		}
		multiManager.serverInstances = append(multiManager.serverInstances, instance)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start all server instances
	var wg sync.WaitGroup
	errorChan := make(chan error, len(multiManager.serverInstances)*3)

	for _, instance := range multiManager.serverInstances {
		wg.Add(1)
		go func(inst *ServerInstance) {
			defer wg.Done()
			if err := startServerInstance(inst, cfg, multiManager.shutdownChan, &wg, errorChan); err != nil {
				errorChan <- fmt.Errorf("server %s error: %w", inst.name, err)
			}
		}(instance)
	}

	logger.Info("All server instances started successfully",
		zap.Int("server_count", len(multiManager.serverInstances)))

	// Wait for shutdown signal or server error
	select {
	case <-sigChan:
		logger.Info("Shutdown signal received, stopping all servers...")
	case err := <-errorChan:
		logger.Error("Server error occurred, shutting down all servers", zap.Error(err))
		cancel()
	}

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	logger.Info("Starting graceful shutdown of all server instances...")

	// Close shutdown channel to signal all components
	close(multiManager.shutdownChan)

	// Shutdown all server instances
	for _, instance := range multiManager.serverInstances {
		go func(inst *ServerInstance) {
			shutdownServerInstance(inst, shutdownCtx, logger)
		}(instance)
	}

	// Signal all goroutines to stop
	cancel()

	// Wait for all servers to stop with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All server instances stopped gracefully")
	case <-time.After(35 * time.Second):
		logger.Warn("Graceful shutdown timeout exceeded, forcing exit")
	}

	logger.Info("Multi-server shutdown completed")
	return nil
}

// setupLogger creates a logger with the specified configuration
func setupLogger(loggingConfig config.LoggingConfig, serverName string) (*zap.Logger, error) {
	// Create log file name
	logFile := fmt.Sprintf("logs/%s.log", serverName)
	if loggingConfig.File != "" {
		logFile = loggingConfig.File
	}

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Configure log level
	var level zapcore.Level
	switch loggingConfig.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	// Configure log rotation
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    100, // MB
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}

	// Create encoder config
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.LevelKey = "level"
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Create core with both file and console output
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(lumberjackLogger),
		level,
	)

	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	core := zapcore.NewTee(fileCore, consoleCore)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return logger, nil
}

// createServerInstance creates a new server instance with its own load balancers
func createServerInstance(serverCfg config.ServerConfig, cfg *config.Config, logger *zap.Logger) (*ServerInstance, error) {
	// Get upstreams for this server
	upstreams := cfg.GetUpstreamsByNames(serverCfg.Upstreams)
	websocketUpstreams := cfg.GetWebSocketUpstreamsByNames(serverCfg.Upstreams)

	// Get per-server configurations (fallback to global if not set)
	lbConfig := cfg.GetLoadBalancerConfig(serverCfg.Name)
	proxyConfig := cfg.GetProxyConfig(serverCfg.Name)
	corsConfig := cfg.GetCORSConfig(serverCfg.Name)

	// Create HTTP load balancer for this server
	lb, err := loadbalancer.NewLoadBalancer(upstreams, lbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP load balancer for server %s: %w", serverCfg.Name, err)
	}

	// Create WebSocket load balancer for this server
	wsLB, err := loadbalancer.NewLoadBalancer(websocketUpstreams, lbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket load balancer for server %s: %w", serverCfg.Name, err)
	}

	// Setup per-server logger
	loggingConfig := cfg.GetLoggingConfig(serverCfg.Name)
	serverLogger, err := setupLogger(loggingConfig, serverCfg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to setup logger for server %s: %w", serverCfg.Name, err)
	}

	// Create proxy server
	proxyServer := proxy.NewProxyServer(lb, wsLB, serverLogger, proxyConfig, corsConfig)

	instance := &ServerInstance{
		name:           serverCfg.Name,
		config:         serverCfg,
		loadBalancer:   lb,
		wsLoadBalancer: wsLB,
		proxyServer:    proxyServer,
		gnetStarted:    make(chan struct{}),
		logger:         serverLogger,
	}

	return instance, nil
}

// startServerInstance starts a server instance
func startServerInstance(instance *ServerInstance, cfg *config.Config, shutdownChan <-chan struct{}, wg *sync.WaitGroup, errorChan chan<- error) error {
	instance.logger.Info("Starting server instance",
		zap.String("name", instance.name),
		zap.String("address", fmt.Sprintf("%s:%d", instance.config.Host, instance.config.Port)))

	// Add to wait group before starting goroutine
	wg.Add(1)

	// Start gnet server
	go func() {
		defer wg.Done()
		addr := fmt.Sprintf("tcp://%s:%d", instance.config.Host, instance.config.Port)
		instance.logger.Info("Reverse proxy server started successfully",
			zap.String("server", instance.name),
			zap.String("address", addr))

		if err := gnet.Run(instance.proxyServer, addr, gnet.WithMulticore(true)); err != nil {
			select {
			case <-shutdownChan:
				// Shutdown was requested, this is expected
				instance.logger.Info("Server shutdown completed", zap.String("server", instance.name))
			default:
				// Unexpected error
				errorChan <- fmt.Errorf("gnet server error for %s: %w", instance.name, err)
			}
		}
	}()

	// Signal that gnet server has started
	close(instance.gnetStarted)

	return nil
}

// shutdownServerInstance gracefully shuts down a server instance
func shutdownServerInstance(instance *ServerInstance, ctx context.Context, logger *zap.Logger) {
	logger.Info("Shutting down server instance", zap.String("name", instance.name))

	// Stop load balancers first to prevent panic from double close
	if instance.loadBalancer != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Warn("Recovered from panic during load balancer shutdown",
						zap.String("server", instance.name),
						zap.Any("panic", r))
				}
			}()
			instance.loadBalancer.StopHealthCheck()
		}()
	}
	if instance.wsLoadBalancer != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Warn("Recovered from panic during WebSocket load balancer shutdown",
						zap.String("server", instance.name),
						zap.Any("panic", r))
				}
			}()
			instance.wsLoadBalancer.StopHealthCheck()
		}()
	}

	// Shutdown proxy server
	if instance.proxyServer != nil {
		if err := instance.proxyServer.Shutdown(ctx); err != nil {
			logger.Error("Error shutting down proxy server",
				zap.String("server", instance.name),
				zap.Error(err))
		}
	}

	logger.Info("Server instance shutdown completed", zap.String("name", instance.name))
}


