package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"surikiti/config"
	"surikiti/loadbalancer"
	"surikiti/proxy"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.toml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Setup logger
	logger, err := setupLogger(cfg.Logging)
	if err != nil {
		log.Fatalf("Failed to setup logger: %v", err)
	}
	defer logger.Sync()

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx // Will be used in server startup

	logger.Info("Starting Surikiti Reverse Proxy",
		zap.String("version", "1.0.0"),
		zap.String("config", *configPath))

	// Create HTTP load balancer
	lb, err := loadbalancer.NewLoadBalancer(cfg.Upstreams, cfg.LoadBalancer)
	if err != nil {
		logger.Fatal("Failed to create HTTP load balancer", zap.Error(err))
	}

	// Create WebSocket load balancer
	wsLB, err := loadbalancer.NewWebSocketLoadBalancer(cfg.WebSocketUpstreams, cfg.LoadBalancer)
	if err != nil {
		logger.Fatal("Failed to create WebSocket load balancer", zap.Error(err))
	}

	// Start health checks
	lb.StartHealthCheck()
	wsLB.StartHealthCheck()
	logger.Info("Health check started for upstream servers")

	// Create proxy server
	proxyServer := proxy.NewProxyServer(lb, wsLB, logger, cfg.Proxy, cfg.CORS)

	// Server management structure
	type ServerManager struct {
		gnetEngine    gnet.Engine
		httpServer    *http.Server
		websocketServer *http.Server
		mu            sync.RWMutex
		shutdownChan  chan struct{}
		gnetStarted   chan struct{}
	}

	serverManager := &ServerManager{
		shutdownChan: make(chan struct{}),
		gnetStarted:  make(chan struct{}),
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start gnet server
	serverAddr := cfg.GetServerAddress()
	logger.Info("Starting reverse proxy server",
		zap.String("address", serverAddr),
		zap.String("load_balancer_method", cfg.LoadBalancer.Method),
		zap.Int("upstream_count", len(cfg.Upstreams)))

	// Log HTTP upstream servers
	for _, upstream := range cfg.Upstreams {
		logger.Info("Registered HTTP upstream server",
			zap.String("name", upstream.Name),
			zap.String("url", upstream.URL),
			zap.Int("weight", upstream.Weight))
	}

	// Log WebSocket upstream servers
	for _, upstream := range cfg.WebSocketUpstreams {
		logger.Info("Registered WebSocket upstream server",
			zap.String("name", upstream.Name),
			zap.String("url", upstream.URL),
			zap.Int("weight", upstream.Weight))
	}

	// Start servers with graceful shutdown support
	var wg sync.WaitGroup
	errorChan := make(chan error, 3)
	
	// Start server based on WebSocket configuration
	if cfg.Proxy.EnableWebSocket {
		websocketPort := cfg.Server.WebSocketPort
		
		// Check if WebSocket port is the same as HTTP port
		if websocketPort == cfg.Server.Port {
			// Use HTTP server for both HTTP and WebSocket on the same port
			logger.Info("Using HTTP server for both HTTP and WebSocket", zap.Int("port", websocketPort))
			
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if proxyServer.IsWebSocketRequest(r) {
					// Handle WebSocket upgrade
					proxyServer.HandleWebSocketHTTP(w, r)
				} else {
					// Handle regular HTTP proxy
					proxyServer.HandleHTTPProxy(w, r)
				}
			})
			
			serverManager.httpServer = &http.Server{
				Addr:    serverAddr,
				Handler: mux,
				ReadTimeout:  cfg.Proxy.RequestTimeout,
				WriteTimeout: cfg.Proxy.ResponseTimeout,
				IdleTimeout:  cfg.Proxy.KeepAliveTimeout,
			}
			
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Info("Starting unified HTTP/WebSocket server", zap.String("address", serverAddr))
				if err := serverManager.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					errorChan <- fmt.Errorf("unified server error: %w", err)
				}
			}()
			
			logger.Info("Unified HTTP/WebSocket server started successfully", zap.String("address", serverAddr))
		} else {
			// Use separate ports: gnet for HTTP, standard HTTP server for WebSocket
			websocketAddr := cfg.Server.Host + ":" + strconv.Itoa(websocketPort)
			
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if proxyServer.IsWebSocketRequest(r) {
					proxyServer.HandleWebSocketHTTP(w, r)
				} else {
					http.Error(w, "WebSocket connections only", http.StatusBadRequest)
				}
			})
			
			serverManager.websocketServer = &http.Server{
				Addr:    websocketAddr,
				Handler: mux,
				ReadTimeout:  cfg.Proxy.RequestTimeout,
				WriteTimeout: cfg.Proxy.ResponseTimeout,
				IdleTimeout:  cfg.Proxy.KeepAliveTimeout,
			}
			
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Info("Starting WebSocket server", zap.String("address", websocketAddr))
				if err := serverManager.websocketServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					errorChan <- fmt.Errorf("websocket server error: %w", err)
				}
			}()
			
			// Give WebSocket server time to start before starting gnet server
			time.Sleep(100 * time.Millisecond)
			
			// Start gnet server for HTTP proxy
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Info("Starting gnet HTTP server", zap.String("address", serverAddr))
				close(serverManager.gnetStarted) // Signal that gnet is starting
				if err := gnet.Run(proxyServer, "tcp://"+serverAddr,
					gnet.WithMulticore(true),
					gnet.WithReusePort(true),
					gnet.WithTCPKeepAlive(time.Minute*2),
					gnet.WithTCPNoDelay(gnet.TCPNoDelay),
					gnet.WithSocketRecvBuffer(64*1024),
					gnet.WithSocketSendBuffer(64*1024),
					gnet.WithReadBufferCap(16*1024),
					gnet.WithWriteBufferCap(16*1024),
					gnet.WithLockOSThread(true),
				); err != nil {
					// Only report error if it's not due to graceful shutdown
					select {
					case <-serverManager.shutdownChan:
						// Graceful shutdown, not an error
						logger.Info("Gnet server stopped gracefully")
					default:
						errorChan <- fmt.Errorf("gnet server error: %w", err)
					}
				}
			}()
			
			logger.Info("Reverse proxy servers started successfully", 
				zap.String("http_address", serverAddr),
				zap.String("websocket_address", websocketAddr))
		}
	} else {
		// WebSocket disabled, use only gnet for HTTP
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("Starting gnet HTTP server", zap.String("address", serverAddr))
			close(serverManager.gnetStarted) // Signal that gnet is starting
			if err := gnet.Run(proxyServer, "tcp://"+serverAddr,
				gnet.WithMulticore(true),
				gnet.WithReusePort(true),
				gnet.WithTCPKeepAlive(time.Minute*2),
				gnet.WithTCPNoDelay(gnet.TCPNoDelay),
				gnet.WithSocketRecvBuffer(64*1024),
				gnet.WithSocketSendBuffer(64*1024),
				gnet.WithReadBufferCap(16*1024),
				gnet.WithWriteBufferCap(16*1024),
				gnet.WithLockOSThread(true),
			); err != nil {
				// Only report error if it's not due to graceful shutdown
				select {
				case <-serverManager.shutdownChan:
					// Graceful shutdown, not an error
					logger.Info("Gnet server stopped gracefully")
				default:
					errorChan <- fmt.Errorf("gnet server error: %w", err)
				}
			}
		}()
		
		logger.Info("Reverse proxy server started successfully", zap.String("address", serverAddr))
	}



	// Wait for gnet to start before proceeding
	<-serverManager.gnetStarted

	// Wait for shutdown signal or server error
	select {
	case <-sigChan:
		logger.Info("Shutdown signal received, stopping server...")
	case err := <-errorChan:
		logger.Error("Server error occurred, shutting down", zap.Error(err))
		cancel()
	}

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	logger.Info("Starting graceful shutdown...")

	// Close shutdown channel to signal all components
	close(serverManager.shutdownChan)

	// Shutdown proxy server first (includes gnet engine)
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error shutting down proxy server", zap.Error(err))
	}

	// Shutdown HTTP servers
	if serverManager.httpServer != nil {
		logger.Info("Shutting down HTTP server")
		if err := serverManager.httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Error shutting down HTTP server", zap.Error(err))
		}
	}

	if serverManager.websocketServer != nil {
		logger.Info("Shutting down WebSocket server")
		if err := serverManager.websocketServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Error shutting down WebSocket server", zap.Error(err))
		}
	}

	// Stop WebSocket load balancer health checks
	if wsLB != nil {
		wsLB.StopHealthCheck()
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
		logger.Info("All servers stopped gracefully")
	case <-time.After(35 * time.Second):
		logger.Warn("Graceful shutdown timeout exceeded, forcing exit")
	}

	logger.Info("Server shutdown completed")
	os.Exit(0)
}

func setupLogger(logConfig config.LoggingConfig) (*zap.Logger, error) {
	// Configure log level
	var level zap.AtomicLevel
	switch logConfig.Level {
	case "debug":
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Configure encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.LevelKey = "level"
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Configure core with file rotation
	fileWriter := &lumberjack.Logger{
		Filename:   logConfig.File,
		MaxSize:    100, // MB
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(fileWriter),
		),
		level,
	)

	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel)), nil
}