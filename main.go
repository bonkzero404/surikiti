package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
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

	logger.Info("Starting Surikiti Reverse Proxy",
		zap.String("version", "1.0.0"),
		zap.String("config", *configPath))

	// Create load balancer
	lb, err := loadbalancer.NewLoadBalancer(cfg.Upstreams, cfg.LoadBalancer)
	if err != nil {
		logger.Fatal("Failed to create load balancer", zap.Error(err))
	}

	// Start health checks
	lb.StartHealthCheck()
	logger.Info("Health check started for upstream servers")

	// Create proxy server
	proxyServer := proxy.NewProxyServer(lb, logger, cfg.Proxy, cfg.CORS)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start gnet server
	serverAddr := cfg.GetServerAddress()
	logger.Info("Starting reverse proxy server",
		zap.String("address", serverAddr),
		zap.String("load_balancer_method", cfg.LoadBalancer.Method),
		zap.Int("upstream_count", len(cfg.Upstreams)))

	// Log upstream servers
	for _, upstream := range cfg.Upstreams {
		logger.Info("Registered upstream server",
			zap.String("name", upstream.Name),
			zap.String("url", upstream.URL),
			zap.Int("weight", upstream.Weight))
	}

	go func() {
		if err := gnet.Run(proxyServer, "tcp://"+serverAddr,
			gnet.WithMulticore(true),
			gnet.WithReusePort(true),
			gnet.WithTCPKeepAlive(time.Minute*2),
			gnet.WithTCPNoDelay(gnet.TCPNoDelay),
		); err != nil {
			logger.Fatal("Failed to start gnet server", zap.Error(err))
		}
	}()

	logger.Info("Reverse proxy server started successfully",
		zap.String("address", serverAddr))

	// Wait for shutdown signal
	<-sigChan
	logger.Info("Shutdown signal received, stopping server...")

	// Graceful shutdown
	logger.Info("Server stopped gracefully")
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