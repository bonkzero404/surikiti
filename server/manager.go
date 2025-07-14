package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
	"surikiti/logger"
	"surikiti/proxy"
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

// NewMultiServerManager creates a new multi-server manager
func NewMultiServerManager() *MultiServerManager {
	return &MultiServerManager{
		shutdownChan: make(chan struct{}),
	}
}

// CreateServerInstance creates a new server instance with its own load balancers
func (msm *MultiServerManager) CreateServerInstance(serverCfg config.ServerConfig, cfg *config.Config, mainLogger *zap.Logger) (*ServerInstance, error) {
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
	serverLogger, err := logger.SetupLogger(loggingConfig, serverCfg.Name)
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

	msm.mu.Lock()
	msm.serverInstances = append(msm.serverInstances, instance)
	msm.mu.Unlock()

	return instance, nil
}

// StartServerInstance starts a server instance
func (msm *MultiServerManager) StartServerInstance(instance *ServerInstance, wg *sync.WaitGroup, errorChan chan<- error) {
	instance.logger.Info("Starting server instance",
		zap.String("name", instance.name),
		zap.String("address", fmt.Sprintf("%s:%d", instance.config.Host, instance.config.Port)))

	// Add to wait group before starting goroutine
	wg.Add(1)

	// Check if this is a WebSocket-only server
	instance.logger.Info("Checking server type", zap.String("name", instance.name), zap.Bool("is_websocket", strings.Contains(strings.ToLower(instance.name), "websocket")))
	if strings.Contains(strings.ToLower(instance.name), "websocket") {
		msm.startWebSocketServer(instance, wg, errorChan)
	} else {
		msm.startGnetServer(instance, wg, errorChan)
	}

	// Signal that server has started
	close(instance.gnetStarted)
}

// startWebSocketServer starts a WebSocket server using standard HTTP server
func (msm *MultiServerManager) startWebSocketServer(instance *ServerInstance, wg *sync.WaitGroup, errorChan chan<- error) {
	go func() {
		defer wg.Done()
		addr := fmt.Sprintf("%s:%d", instance.config.Host, instance.config.Port)
		instance.logger.Info("WebSocket server started successfully",
			zap.String("server", instance.name),
			zap.String("address", fmt.Sprintf("http://%s", addr)))

		// Create HTTP server for WebSocket
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if instance.proxyServer.IsWebSocketRequest(r) {
				instance.proxyServer.HandleWebSocketHTTP(w, r)
			} else {
				instance.proxyServer.HandleHTTPProxy(w, r)
			}
		})

		server := &http.Server{
			Addr:    addr,
			Handler: mux,
		}

		// Store server reference for shutdown
		instance.httpServer = server

		// Start server in a separate goroutine
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errorChan <- fmt.Errorf("HTTP server error for %s: %w", instance.name, err)
			}
		}()

		// Wait for shutdown signal
		<-msm.shutdownChan
		instance.logger.Info("WebSocket server shutdown signal received", zap.String("server", instance.name))
	}()
}

// startGnetServer starts a gnet server for regular HTTP
func (msm *MultiServerManager) startGnetServer(instance *ServerInstance, wg *sync.WaitGroup, errorChan chan<- error) {
	go func() {
		defer wg.Done()
		addr := fmt.Sprintf("tcp://%s:%d", instance.config.Host, instance.config.Port)
		instance.logger.Info("Reverse proxy server started successfully",
			zap.String("server", instance.name),
			zap.String("address", addr))

		if err := gnet.Run(instance.proxyServer, addr, gnet.WithMulticore(true)); err != nil {
			select {
			case <-msm.shutdownChan:
				// Shutdown was requested, this is expected
				instance.logger.Info("Server shutdown completed", zap.String("server", instance.name))
			default:
				// Unexpected error
				errorChan <- fmt.Errorf("gnet server error for %s: %w", instance.name, err)
			}
		}
	}()
}

// StartAllServers starts all server instances
func (msm *MultiServerManager) StartAllServers() (chan error, *sync.WaitGroup) {
	var wg sync.WaitGroup
	errorChan := make(chan error, len(msm.serverInstances)*3)

	msm.mu.RLock()
	for _, instance := range msm.serverInstances {
		msm.StartServerInstance(instance, &wg, errorChan)
	}
	msm.mu.RUnlock()

	return errorChan, &wg
}

// Shutdown gracefully shuts down all server instances
func (msm *MultiServerManager) Shutdown(ctx context.Context, mainLogger *zap.Logger) {
	mainLogger.Info("Starting graceful shutdown of all server instances...")

	// Close shutdown channel to signal all components
	close(msm.shutdownChan)

	// Shutdown all server instances
	msm.mu.RLock()
	for _, instance := range msm.serverInstances {
		go msm.shutdownServerInstance(instance, ctx, mainLogger)
	}
	msm.mu.RUnlock()
}

// shutdownServerInstance gracefully shuts down a server instance
func (msm *MultiServerManager) shutdownServerInstance(instance *ServerInstance, ctx context.Context, mainLogger *zap.Logger) {
	mainLogger.Info("Shutting down server instance", zap.String("name", instance.name))

	// Shutdown HTTP server if it exists (for WebSocket servers)
	if instance.httpServer != nil {
		mainLogger.Info("Shutting down HTTP server", zap.String("server", instance.name))
		if err := instance.httpServer.Shutdown(ctx); err != nil {
			mainLogger.Error("Error shutting down HTTP server",
				zap.String("server", instance.name),
				zap.Error(err))
		}
	}

	// Stop load balancers first to prevent panic from double close
	if instance.loadBalancer != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					mainLogger.Warn("Recovered from panic during load balancer shutdown",
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
					mainLogger.Warn("Recovered from panic during WebSocket load balancer shutdown",
						zap.String("server", instance.name),
						zap.Any("panic", r))
				}
			}()
			instance.wsLoadBalancer.StopHealthCheck()
		}()
	}

	// Shutdown proxy server (for gnet servers)
	if instance.proxyServer != nil {
		if err := instance.proxyServer.Shutdown(ctx); err != nil {
			mainLogger.Error("Error shutting down proxy server",
				zap.String("server", instance.name),
				zap.Error(err))
		}
	}

	mainLogger.Info("Server instance shutdown completed", zap.String("name", instance.name))
}

// GetServerInstances returns a copy of server instances
func (msm *MultiServerManager) GetServerInstances() []*ServerInstance {
	msm.mu.RLock()
	defer msm.mu.RUnlock()

	instances := make([]*ServerInstance, len(msm.serverInstances))
	copy(instances, msm.serverInstances)
	return instances
}
