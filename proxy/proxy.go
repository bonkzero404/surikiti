package proxy

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

type ProxyServer struct {
	mu               sync.RWMutex
	loadBalancer     *loadbalancer.LoadBalancer
	logger           *zap.Logger
	client           *fasthttp.Client
	httpClient       *http.Client
	proxyConfig      config.ProxyConfig
	corsConfig       config.CORSConfig
	websocketHandler *WebSocketHandler
	httpHandler      *HTTPHandler
	http2http3Server *HTTP2HTTP3Server
	engine           gnet.Engine
	engineSet        bool
}

func NewProxyServer(lb *loadbalancer.LoadBalancer, wsLB *loadbalancer.LoadBalancer, logger *zap.Logger, proxyConfig config.ProxyConfig, corsConfig config.CORSConfig) *ProxyServer {
	// Create fasthttp client optimized for stability
	client := &fasthttp.Client{
		ReadTimeout:                   proxyConfig.RequestTimeout,
		WriteTimeout:                  proxyConfig.RequestTimeout,
		MaxIdleConnDuration:           time.Second * 30,
		MaxConnDuration:               time.Minute * 1,
		MaxConnsPerHost:               proxyConfig.MaxConnsPerHost,
		MaxConnWaitTimeout:            time.Second * 5,
		ReadBufferSize:                proxyConfig.BufferSize,
		WriteBufferSize:               proxyConfig.BufferSize,
		DisableHeaderNamesNormalizing: false,
		DisablePathNormalizing:        false,
		RetryIf: func(request *fasthttp.Request) bool {
			// Disable retries for stability
			return false
		},
		Dial: (&fasthttp.TCPDialer{
			Concurrency:      1000,
			DNSCacheDuration: time.Minute * 10,
		}).Dial,
	}

	// Create reusable HTTP client for standard HTTP proxy
	httpClient := &http.Client{
		Timeout: proxyConfig.RequestTimeout * 2, // Give more time for the overall request
		Transport: &http.Transport{
			MaxIdleConns:        proxyConfig.MaxIdleConns,
			MaxIdleConnsPerHost: proxyConfig.MaxIdleConnsPerHost,
			MaxConnsPerHost:     proxyConfig.MaxConnsPerHost,
			IdleConnTimeout:     proxyConfig.IdleConnTimeout,
			DialContext: (&net.Dialer{
				Timeout:   proxyConfig.RequestTimeout,
				KeepAlive: proxyConfig.KeepAliveTimeout,
			}).DialContext,
			TLSHandshakeTimeout: proxyConfig.RequestTimeout,
			DisableKeepAlives:   false, // Enable keep-alives for better performance
			ForceAttemptHTTP2:   false, // Disable HTTP/2 for upstream connections
		},
	}

	ps := &ProxyServer{
		loadBalancer: lb,
		logger:       logger,
		client:       client,
		httpClient:   httpClient,
		proxyConfig:  proxyConfig,
		corsConfig:   corsConfig,
	}

	// Initialize WebSocket handler if enabled
	if proxyConfig.EnableWebSocket {
		ps.websocketHandler = NewWebSocketHandler(wsLB, logger, proxyConfig)
		logger.Info("WebSocket handler enabled")
	}

	// Initialize HTTP handler
	ps.httpHandler = NewHTTPHandler(lb, client, httpClient, logger, proxyConfig, corsConfig)

	// Initialize HTTP/2 and HTTP/3 server if enabled
	if proxyConfig.EnableHTTP2 || proxyConfig.EnableHTTP3 {
		ps.http2http3Server = NewHTTP2HTTP3Server(lb, logger, proxyConfig)
		logger.Info("HTTP/2 and HTTP/3 support enabled")
	}

	// Start health check
	lb.StartHealthCheck()

	return ps
}

func (ps *ProxyServer) OnBoot(eng gnet.Engine) gnet.Action {
	ps.mu.Lock()
	ps.engine = eng
	ps.engineSet = true
	ps.mu.Unlock()
	
	ps.logger.Info("Proxy server started")
	
	// Start HTTP/2 server if enabled
	if ps.http2http3Server != nil && ps.proxyConfig.EnableHTTP2 {
		go func() {
			if ps.proxyConfig.TLSCertFile != "" && ps.proxyConfig.TLSKeyFile != "" {
				addr := "0.0.0.0:8443"
				if err := ps.http2http3Server.StartHTTP2Server(addr); err != nil {
					ps.logger.Error("Failed to start HTTP/2 server", zap.Error(err))
				}
			} else {
				ps.logger.Warn("HTTP/2 enabled but TLS certificates not configured")
			}
		}()
	}
	
	// Start HTTP/3 server if enabled
	if ps.http2http3Server != nil && ps.proxyConfig.EnableHTTP3 {
		go func() {
			if ps.proxyConfig.TLSCertFile != "" && ps.proxyConfig.TLSKeyFile != "" {
				if err := ps.http2http3Server.StartHTTP3Server(); err != nil {
					ps.logger.Error("Failed to start HTTP/3 server", zap.Error(err))
				}
			} else {
				ps.logger.Warn("HTTP/3 enabled but TLS certificates not configured")
			}
		}()
	}
	
	return gnet.None
}

func (ps *ProxyServer) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	ps.logger.Debug("New connection opened", zap.String("remote", c.RemoteAddr().String()))
	return nil, gnet.None
}

func (ps *ProxyServer) OnClose(c gnet.Conn, err error) gnet.Action {
	if err != nil {
		// These errors are normal when client closes connection
		errorMsg := err.Error()
		if errorMsg == "EOF" ||
			strings.Contains(errorMsg, "read: EOF") ||
			strings.Contains(errorMsg, "connection reset by peer") ||
			strings.Contains(errorMsg, "broken pipe") {
			ps.logger.Debug("Connection closed by client",
				zap.String("remote", c.RemoteAddr().String()),
				zap.String("reason", errorMsg))
		} else {
			ps.logger.Error("Connection closed with unexpected error",
				zap.Error(err),
				zap.String("remote", c.RemoteAddr().String()))
		}
	} else {
		ps.logger.Debug("Connection closed gracefully", zap.String("remote", c.RemoteAddr().String()))
	}
	return gnet.None
}

func (ps *ProxyServer) OnShutdown(eng gnet.Engine) {
	ps.logger.Info("Proxy server shutting down")
}

func (ps *ProxyServer) Shutdown(ctx context.Context) error {
	ps.logger.Info("Starting proxy server shutdown")
	
	// Stop gnet engine
	ps.mu.RLock()
	engine := ps.engine
	engineSet := ps.engineSet
	ps.mu.RUnlock()
	
	if engineSet {
		ps.logger.Info("Stopping gnet engine")
		if err := engine.Stop(ctx); err != nil {
			ps.logger.Error("Error stopping gnet engine", zap.Error(err))
		}
	}
	
	// Stop health checks safely
	if ps.loadBalancer != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					ps.logger.Warn("Recovered from panic during load balancer shutdown", zap.Any("panic", r))
				}
			}()
			ps.loadBalancer.StopHealthCheck()
		}()
	}
	
	// Shutdown HTTP/2 and HTTP/3 servers
	if ps.http2http3Server != nil {
		if err := ps.http2http3Server.Shutdown(ctx); err != nil {
			ps.logger.Error("Error shutting down HTTP/2/HTTP/3 servers", zap.Error(err))
		}
	}
	
	// Close fasthttp client connections
	if ps.client != nil {
		ps.client.CloseIdleConnections()
	}
	
	// Close HTTP client connections
	if ps.httpClient != nil {
		ps.httpClient.CloseIdleConnections()
	}
	
	ps.logger.Info("Proxy server shutdown completed")
	return nil
}

func (ps *ProxyServer) OnTick() (delay time.Duration, action gnet.Action) {
	return time.Second, gnet.None
}

// IsWebSocketRequest checks if the HTTP request is a WebSocket upgrade request
func (ps *ProxyServer) IsWebSocketRequest(r *http.Request) bool {
	if ps.websocketHandler == nil {
		return false
	}
	
	return ps.websocketHandler.IsWebSocketRequest(r)
}

// HandleWebSocketHTTP handles WebSocket connections through standard HTTP server
func (ps *ProxyServer) HandleWebSocketHTTP(w http.ResponseWriter, r *http.Request) {
	if ps.websocketHandler == nil {
		http.Error(w, "WebSocket proxy not initialized", http.StatusInternalServerError)
		return
	}
	
	ps.websocketHandler.HandleWebSocketHTTP(w, r)
}

// HandleHTTPProxy handles regular HTTP proxy requests using standard HTTP server
func (ps *ProxyServer) HandleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	if ps.httpHandler == nil {
		http.Error(w, "HTTP handler not initialized", http.StatusInternalServerError)
		return
	}
	
	ps.httpHandler.HandleHTTPProxy(w, r)
}

func (ps *ProxyServer) OnTraffic(c gnet.Conn) gnet.Action {
	// Read the HTTP request
	reqData, err := c.Next(-1)
	if err != nil {
		ps.logger.Debug("Failed to read request data", zap.Error(err))
		return gnet.Close
	}

	// Check for WebSocket upgrade request
	if ps.websocketHandler != nil && ps.proxyConfig.EnableWebSocket {
		// Parse headers to check for WebSocket upgrade
		headers := make(map[string]string)
		// Simple header parsing for WebSocket detection
		lines := strings.Split(string(reqData), "\r\n")
		for _, line := range lines {
			if strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		}
		
		if ps.websocketHandler.IsWebSocketRequestFromHeaders(headers) {
			ps.logger.Debug("WebSocket upgrade request detected")
			// Note: WebSocket handling would require a different approach
			// as gnet doesn't directly support HTTP upgrade protocol
			// This is a limitation that would need to be addressed
			ps.sendErrorResponse(c, fasthttp.StatusNotImplemented, "WebSocket upgrade not supported in gnet mode")
			return gnet.None
		}
	}

	// Delegate to HTTP handler
	if ps.httpHandler != nil {
		return ps.httpHandler.HandleTraffic(c, reqData)
	}

	// Fallback error response
	ps.sendErrorResponse(c, fasthttp.StatusInternalServerError, "Internal Server Error")
	return gnet.None
}









func (ps *ProxyServer) sendErrorResponse(c gnet.Conn, statusCode int, message string) {
	if ps.httpHandler != nil {
		ps.httpHandler.sendErrorResponse(c, statusCode, message)
	}
}
