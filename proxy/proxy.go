package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

type ProxyServer struct {
	loadBalancer *loadbalancer.LoadBalancer
	logger       *zap.Logger
	client       *fasthttp.Client
	proxyConfig  config.ProxyConfig
	corsConfig   config.CORSConfig
	websocketProxy *WebSocketProxy
	http2http3Server *HTTP2HTTP3Server
}

func NewProxyServer(lb *loadbalancer.LoadBalancer, logger *zap.Logger, proxyConfig config.ProxyConfig, corsConfig config.CORSConfig) *ProxyServer {
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

	ps := &ProxyServer{
		loadBalancer: lb,
		logger:       logger,
		client:       client,
		proxyConfig:  proxyConfig,
		corsConfig:   corsConfig,
	}

	// Initialize WebSocket proxy if enabled
	if proxyConfig.EnableWebSocket {
		ps.websocketProxy = NewWebSocketProxy(lb, logger, proxyConfig)
		logger.Info("WebSocket proxy enabled")
	}

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

func (ps *ProxyServer) OnTick() (delay time.Duration, action gnet.Action) {
	return time.Second, gnet.None
}

// IsWebSocketRequest checks if the HTTP request is a WebSocket upgrade request
func (ps *ProxyServer) IsWebSocketRequest(r *http.Request) bool {
	if ps.websocketProxy == nil {
		return false
	}
	
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	
	return ps.websocketProxy.IsWebSocketRequest(headers)
}

// HandleWebSocketHTTP handles WebSocket connections through standard HTTP server
func (ps *ProxyServer) HandleWebSocketHTTP(w http.ResponseWriter, r *http.Request) {
	if ps.websocketProxy == nil {
		http.Error(w, "WebSocket proxy not initialized", http.StatusInternalServerError)
		return
	}
	
	err := ps.websocketProxy.HandleWebSocket(w, r)
	if err != nil {
		ps.logger.Error("WebSocket proxy error", zap.Error(err))
		// Don't write error response here as HandleWebSocket may have already written to the connection
	}
}

func (ps *ProxyServer) OnTraffic(c gnet.Conn) gnet.Action {
	// Read the HTTP request
	reqData, err := c.Next(-1)
	if err != nil {
		ps.logger.Debug("Failed to read request data", zap.Error(err))
		return gnet.Close
	}

	// Check for empty request data
	if len(reqData) == 0 {
		ps.logger.Debug("Received empty request data")
		return gnet.Close
	}

	// Check max body size first
	if int64(len(reqData)) > ps.proxyConfig.MaxBodySize {
		ps.logger.Warn("Request too large", zap.Int("size", len(reqData)), zap.Int64("max", ps.proxyConfig.MaxBodySize))
		ps.sendErrorResponse(c, fasthttp.StatusRequestEntityTooLarge, "Request Entity Too Large")
		return gnet.None
	}

	// Parse HTTP request using fasthttp properly
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	bufReader := bufio.NewReader(bytes.NewReader(reqData))
	if readErr := req.Read(bufReader); readErr != nil {
		ps.logger.Debug("Failed to parse HTTP request", zap.Error(readErr))
		ps.sendErrorResponse(c, fasthttp.StatusBadRequest, "Bad Request")
		return gnet.None
	}

	// Validate HTTP method
	method := string(req.Header.Method())
	if method == "" {
		ps.logger.Debug("Missing HTTP method in request")
		ps.sendErrorResponse(c, fasthttp.StatusBadRequest, "Bad Request")
		return gnet.None
	}

	// Check for WebSocket upgrade request
	if ps.websocketProxy != nil && ps.proxyConfig.EnableWebSocket {
		headers := make(map[string]string)
		req.Header.VisitAll(func(key, value []byte) {
			headers[string(key)] = string(value)
		})
		
		if ps.websocketProxy.IsWebSocketRequest(headers) {
			ps.logger.Debug("WebSocket upgrade request detected")
			// Note: WebSocket handling would require a different approach
			// as gnet doesn't directly support HTTP upgrade protocol
			// This is a limitation that would need to be addressed
			ps.sendErrorResponse(c, fasthttp.StatusNotImplemented, "WebSocket upgrade not supported in gnet mode")
			return gnet.None
		}
	}

	// Handle CORS preflight requests
	if ps.handleCORS(req, c) {
		return gnet.None
	}

	// Get upstream server
	upstream := ps.loadBalancer.GetUpstream()
	if upstream == nil {
		ps.sendErrorResponse(c, fasthttp.StatusServiceUnavailable, "Service Unavailable")
		return gnet.None
	}

	// Increment connection count
	ps.loadBalancer.IncreaseConnections(upstream)
	defer ps.loadBalancer.DecreaseConnections(upstream)

	// Forward request to upstream
	resp, err := ps.forwardRequest(req, upstream)
	if err != nil {
		ps.sendErrorResponse(c, fasthttp.StatusBadGateway, "Bad Gateway")
		return gnet.None
	}
	defer fasthttp.ReleaseResponse(resp)

	// Send response back to client using fasthttp response writer
	if err := ps.sendResponse(c, resp); err != nil {
		return gnet.Close
	}

	return gnet.None
}

// handleCORS adds CORS headers to the response if CORS is enabled
func (ps *ProxyServer) handleCORS(req *fasthttp.Request, c gnet.Conn) bool {
	if !ps.corsConfig.Enabled {
		return false
	}

	origin := string(req.Header.Peek("Origin"))
	method := string(req.Header.Method())

	// Check if origin is allowed
	allowedOrigin := "*"
	if len(ps.corsConfig.AllowedOrigins) > 0 && ps.corsConfig.AllowedOrigins[0] != "*" {
		originAllowed := false
		if slices.Contains(ps.corsConfig.AllowedOrigins, origin) {
			allowedOrigin = origin
			originAllowed = true
		}
		if !originAllowed {
			return false
		}
	}

	// Handle preflight request using fasthttp response
	if method == "OPTIONS" {
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(resp)

		resp.SetStatusCode(fasthttp.StatusOK)
		resp.Header.Set("Access-Control-Allow-Origin", allowedOrigin)
		resp.Header.Set("Access-Control-Allow-Methods", strings.Join(ps.corsConfig.AllowedMethods, ", "))
		resp.Header.Set("Access-Control-Allow-Headers", strings.Join(ps.corsConfig.AllowedHeaders, ", "))
		if ps.corsConfig.AllowCredentials {
			resp.Header.Set("Access-Control-Allow-Credentials", "true")
		}
		resp.Header.Set("Access-Control-Max-Age", strconv.Itoa(ps.corsConfig.MaxAge))
		resp.Header.Set("Content-Length", "0")

		// Write response using fasthttp
		ps.writeResponse(c, resp)
		return true
	}

	return false
}

func (ps *ProxyServer) forwardRequest(req *fasthttp.Request, upstream *loadbalancer.Upstream) (*fasthttp.Response, error) {
	// Create fasthttp response
	fastResp := fasthttp.AcquireResponse()

	// Build target URL
	originalURI := req.RequestURI()
	targetURI := upstream.URL.String() + string(originalURI)
	req.SetRequestURI(targetURI)

	// Add proxy headers
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", string(req.Header.Host()))
	req.Header.Set("X-Real-IP", "127.0.0.1")

	// Keep connection alive for better performance
	req.Header.Set("Connection", "keep-alive")

	// Execute request with minimal retry logic for performance
	maxRetries := 2
	var err error
	for i := 0; i < maxRetries; i++ {
		err = ps.client.Do(req, fastResp)
		if err == nil {
			return fastResp, nil
		}

		// Mark upstream as unhealthy on persistent errors
		if i == maxRetries-1 {
			ps.loadBalancer.MarkUnhealthy(upstream)
		}

		// Minimal delay before retry
		time.Sleep(time.Millisecond * 10)
	}

	fasthttp.ReleaseResponse(fastResp)
	return nil, fmt.Errorf("failed to execute request after %d retries: %w", maxRetries, err)
}

func (ps *ProxyServer) sendResponse(c gnet.Conn, resp *fasthttp.Response) error {
	// Add CORS headers if enabled
	if ps.corsConfig.Enabled {
		resp.Header.Set("Access-Control-Allow-Origin", "*")
		if len(ps.corsConfig.ExposedHeaders) > 0 {
			resp.Header.Set("Access-Control-Expose-Headers", strings.Join(ps.corsConfig.ExposedHeaders, ", "))
		}
		if ps.corsConfig.AllowCredentials {
			resp.Header.Set("Access-Control-Allow-Credentials", "true")
		}
	}

	return ps.writeResponse(c, resp)
}

// writeResponse efficiently writes fasthttp response to gnet connection
func (ps *ProxyServer) writeResponse(c gnet.Conn, resp *fasthttp.Response) error {
	// Pre-allocate buffer with larger estimated size for better performance
	body := resp.Body()
	estimatedSize := 1024 + len(body) // Larger header estimate + body
	buf := make([]byte, 0, estimatedSize)

	// Status line
	buf = append(buf, fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode(), fasthttp.StatusMessage(resp.StatusCode()))...)

	// Keep connection alive for better performance
	buf = append(buf, "Connection: keep-alive\r\n"...)

	// Headers
	resp.Header.VisitAll(func(key, value []byte) {
		// Skip connection header to avoid conflicts
		if !bytes.EqualFold(key, []byte("connection")) {
			buf = append(buf, key...)
			buf = append(buf, ": "...)
			buf = append(buf, value...)
			buf = append(buf, "\r\n"...)
		}
	})

	// Content-Length if not present
	if len(resp.Header.Peek("Content-Length")) == 0 {
		buf = append(buf, fmt.Sprintf("Content-Length: %d\r\n", len(body))...)
	}

	// End of headers
	buf = append(buf, "\r\n"...)

	// Body
	buf = append(buf, body...)

	_, err := c.Write(buf)
	return err
}

func (ps *ProxyServer) sendErrorResponse(c gnet.Conn, statusCode int, message string) {
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	resp.SetStatusCode(statusCode)
	resp.Header.Set("Content-Type", "text/plain")
	resp.SetBodyString(message)

	ps.writeResponse(c, resp)
}
