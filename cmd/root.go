package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/logger"
	"surikiti/server"
)

var (
	configsDir string
	configFile string
)

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
	rootCmd.Flags().StringVar(&configsDir, "configs", ".", "Path to configuration directory containing TOML files")
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
	globalLogger, err := logger.SetupLogger(cfg.Logging, "global")
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer globalLogger.Sync()

	// Create context for graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if configFile != "" {
		globalLogger.Info("Starting Surikiti Reverse Proxy",
			zap.String("version", "1.0.0"),
			zap.String("config_mode", "single_file"),
			zap.String("config_path", configFile))
	} else {
		globalLogger.Info("Starting Surikiti Reverse Proxy",
			zap.String("version", "1.0.0"),
			zap.String("config_mode", "multi_file"),
			zap.String("config_dir", configsDir))
	}

	// Get enabled servers
	enabledServers := cfg.GetEnabledServers()
	if len(enabledServers) == 0 {
		return fmt.Errorf("no enabled servers found in configuration")
	}

	globalLogger.Info("Found enabled servers", zap.Int("count", len(enabledServers)))

	// Create multi-server manager
	multiManager := server.NewMultiServerManager()

	// Create server instances
	for _, serverCfg := range enabledServers {
		_, err := multiManager.CreateServerInstance(serverCfg, cfg, globalLogger)
		if err != nil {
			return fmt.Errorf("failed to create server instance %s: %w", serverCfg.Name, err)
		}
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start all server instances
	errorChan, wg := multiManager.StartAllServers()

	instances := multiManager.GetServerInstances()
	globalLogger.Info("All server instances started successfully",
		zap.Int("server_count", len(instances)))

	// Wait for shutdown signal or server error
	select {
	case <-sigChan:
		globalLogger.Info("Shutdown signal received, stopping all servers...")
	case err := <-errorChan:
		globalLogger.Error("Server error occurred, shutting down all servers", zap.Error(err))
		cancel()
	}

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown all server instances
	multiManager.Shutdown(shutdownCtx, globalLogger)

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
		globalLogger.Info("All server instances stopped gracefully")
	case <-time.After(35 * time.Second):
		globalLogger.Warn("Graceful shutdown timeout exceeded, forcing exit")
	}

	globalLogger.Info("Multi-server shutdown completed")
	return nil
}
