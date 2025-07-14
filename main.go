/*
Copyright 2024 Surikiti Project

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	configsDir string
	configFile string
)

// printStartupBanner displays a colorful startup banner
func printStartupBanner(version, configMode, configPath string, serverCount int) {
	// Clear screen and move cursor to top
	fmt.Print("\033[2J\033[H")

	// ASCII Art Banner
	cyan := color.New(color.FgCyan, color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	yellow := color.New(color.FgYellow, color.Bold)
	blue := color.New(color.FgBlue, color.Bold)
	magenta := color.New(color.FgMagenta, color.Bold)
	white := color.New(color.FgWhite, color.Bold)

	cyan.Println("")
	cyan.Println("  ███████╗██╗   ██╗██████╗ ██╗██╗  ██╗██╗████████╗██╗")
	cyan.Println("  ██╔════╝██║   ██║██╔══██╗██║██║ ██╔╝██║╚══██╔══╝██║")
	cyan.Println("  ███████╗██║   ██║██████╔╝██║█████╔╝ ██║   ██║   ██║")
	cyan.Println("  ╚════██║██║   ██║██╔══██╗██║██╔═██╗ ██║   ██║   ██║")
	cyan.Println("  ███████║╚██████╔╝██║  ██║██║██║  ██╗██║   ██║   ██║")
	cyan.Println("  ╚══════╝ ╚═════╝ ╚═╝  ╚═╝╚═╝╚═╝  ╚═╝╚═╝   ╚═╝   ╚═╝")
	cyan.Println("")

	// Subtitle
	magenta.Println("           🚀 High-Performance Reverse Proxy Server 🚀")
	fmt.Println()

	// Version and config info
	green.Printf("  ✅ Version: ")
	white.Printf("%s\n", version)

	green.Printf("  📁 Config Mode: ")
	white.Printf("%s\n", configMode)

	green.Printf("  📂 Config Path: ")
	white.Printf("%s\n", configPath)

	green.Printf("  🖥️  Server Instances: ")
	white.Printf("%d\n", serverCount)
	fmt.Println()

	// Features
	yellow.Println("  🌟 Features:")
	blue.Println("     • HTTP/1.1, HTTP/2, HTTP/3 Support")
	blue.Println("     • WebSocket Proxy with Load Balancing")
	blue.Println("     • Advanced Load Balancing Algorithms")
	blue.Println("     • Health Checking & Auto-Recovery")
	blue.Println("     • Multi-Server Configuration")
	blue.Println("     • Graceful Shutdown")
	fmt.Println()

	// Status
	green.Println("  🎯 Status: Starting servers...")
	fmt.Println()
}

// printServerStatus displays server status after startup
func printServerStatus(instances []*ServerInstance) {
	green := color.New(color.FgGreen, color.Bold)
	white := color.New(color.FgWhite, color.Bold)
	cyan := color.New(color.FgCyan)

	green.Println("  ✅ All servers started successfully!")
	fmt.Println()

	green.Println("  📡 Active Server Instances:")
	for _, instance := range instances {
		white.Printf("     • %s: ", instance.config.Name)
		cyan.Printf("%s:%d\n", instance.config.Host, instance.config.Port)
	}
	fmt.Println()

	green.Println("  🔥 Ready to handle requests! Press Ctrl+C to stop.")
	fmt.Println()
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
	rootCmd.Flags().StringVar(&configsDir, "configs", ".", "Path to configuration directory containing TOML files")
	rootCmd.Flags().StringVar(&configFile, "config", "", "Path to single configuration file (legacy mode)")
}

func runServer(cmd *cobra.Command, args []string) error {
	// Load configuration
	var cfg *Config
	var err error

	if configFile != "" {
		// Legacy mode: single config file
		cfg, err = LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	} else {
		// New mode: multiple config files from directory
		cfg, err = LoadMultiFileConfig(configsDir)
		if err != nil {
			return fmt.Errorf("failed to load multi-file config: %w", err)
		}
	}

	// Setup global logger (fallback)
	globalLogger, err := SetupLogger(cfg.Logging, "global")
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer globalLogger.Sync()

	// Create context for graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get enabled servers
	enabledServers := cfg.GetEnabledServers()
	if len(enabledServers) == 0 {
		return fmt.Errorf("no enabled servers found in configuration")
	}

	// Display startup banner instead of logs
	configMode := "single_file"
	configPath := configFile
	if configFile == "" {
		configMode = "multi_file"
		configPath = configsDir
	}
	printStartupBanner("1.0.0", configMode, configPath, len(enabledServers))

	// Create multi-server manager
	multiManager := NewMultiServerManager()

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
	// Display server status with colors instead of logs
	printServerStatus(instances)

	// Wait for shutdown signal or server error
	select {
	case <-sigChan:
		red := color.New(color.FgRed, color.Bold)
		red.Println("\n  🛑 Shutdown signal received, stopping all servers...")
	case err := <-errorChan:
		red := color.New(color.FgRed, color.Bold)
		red.Printf("\n  ❌ Server error occurred: %v\n", err)
		red.Println("  🛑 Shutting down all servers...")
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
		green := color.New(color.FgGreen, color.Bold)
		green.Println("  ✅ All server instances stopped gracefully")
	case <-time.After(35 * time.Second):
		yellow := color.New(color.FgYellow, color.Bold)
		yellow.Println("  ⚠️  Graceful shutdown timeout exceeded, forcing exit")
	}

	green := color.New(color.FgGreen, color.Bold)
	green.Println("  🏁 Multi-server shutdown completed")
	fmt.Println()
	return nil
}

func main() {
	Execute()
}
