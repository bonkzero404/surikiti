package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

type ProxyServer struct {
	loadBalancer *loadbalancer.LoadBalancer
	logger       *zap.Logger
	client       *http.Client
	proxyConfig  config.ProxyConfig
	corsConfig   config.CORSConfig
}

func NewProxyServer(lb *loadbalancer.LoadBalancer, logger *zap.Logger, proxyConfig config.ProxyConfig, corsConfig config.CORSConfig) *ProxyServer {
	// Create custom transport with connection pooling
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: proxyConfig.KeepAliveTimeout,
		}).DialContext,
		MaxIdleConns:        proxyConfig.MaxIdleConns,
		MaxIdleConnsPerHost: proxyConfig.MaxIdleConnsPerHost,
		MaxConnsPerHost:     proxyConfig.MaxConnsPerHost,
		IdleConnTimeout:     proxyConfig.IdleConnTimeout,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   false, // Enable keep-alive
		DisableCompression:  !proxyConfig.EnableCompression,
	}

	return &ProxyServer{
		loadBalancer: lb,
		logger:       logger,
		client: &http.Client{
			Timeout:   proxyConfig.RequestTimeout,
			Transport: transport,
		},
		proxyConfig: proxyConfig,
		corsConfig:  corsConfig,
	}
}

func (ps *ProxyServer) OnBoot(eng gnet.Engine) gnet.Action {
	ps.logger.Info("Proxy server started")
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

func (ps *ProxyServer) OnTraffic(c gnet.Conn) gnet.Action {
	// Read the HTTP request
	reqData, err := c.Next(-1)
	if err != nil {
		ps.logger.Error("Failed to read request data", zap.Error(err))
		return gnet.Close
	}

	// Parse HTTP request
	req, err := ps.parseHTTPRequest(reqData)
	if err != nil {
		ps.logger.Error("Failed to parse HTTP request", zap.Error(err))
		if strings.Contains(err.Error(), "request body too large") {
			ps.sendErrorResponse(c, http.StatusRequestEntityTooLarge, "Request Entity Too Large")
		} else {
			ps.sendErrorResponse(c, http.StatusBadRequest, "Bad Request")
		}
		return gnet.None
	}

	// Handle CORS preflight requests
	if ps.handleCORS(req, c) {
		return gnet.None
	}

	// Get upstream server
	upstream := ps.loadBalancer.GetUpstream()
	if upstream == nil {
		ps.logger.Error("No healthy upstream servers available")
		ps.sendErrorResponse(c, http.StatusServiceUnavailable, "Service Unavailable")
		return gnet.None
	}

	// Increment connection count
	ps.loadBalancer.IncreaseConnections(upstream)
	defer ps.loadBalancer.DecreaseConnections(upstream)

	// Forward request to upstream
	resp, err := ps.forwardRequest(req, upstream)
	if err != nil {
		ps.logger.Error("Failed to forward request",
			zap.Error(err),
			zap.String("upstream", upstream.Name))
		ps.sendErrorResponse(c, http.StatusBadGateway, "Bad Gateway")
		return gnet.None
	}
	defer resp.Body.Close()

	// Send response back to client
	if err := ps.sendResponse(c, resp); err != nil {
		ps.logger.Error("Failed to send response", zap.Error(err))
		return gnet.Close
	}

	ps.logger.Info("Request proxied successfully",
		zap.String("method", req.Method),
		zap.String("path", req.URL.Path),
		zap.String("upstream", upstream.Name),
		zap.Int("status", resp.StatusCode))

	return gnet.None
}

func (ps *ProxyServer) parseHTTPRequest(data []byte) (*http.Request, error) {
	// Check max body size
	if int64(len(data)) > ps.proxyConfig.MaxBodySize {
		return nil, fmt.Errorf("request body too large: %d bytes (max: %d)", len(data), ps.proxyConfig.MaxBodySize)
	}

	reader := bytes.NewReader(data)
	bufReader := bufio.NewReader(reader)
	return http.ReadRequest(bufReader)
}

// handleCORS adds CORS headers to the response if CORS is enabled
func (ps *ProxyServer) handleCORS(req *http.Request, c gnet.Conn) bool {
	if !ps.corsConfig.Enabled {
		return false
	}

	origin := req.Header.Get("Origin")
	method := req.Method

	// Check if origin is allowed
	allowedOrigin := "*"
	if len(ps.corsConfig.AllowedOrigins) > 0 && ps.corsConfig.AllowedOrigins[0] != "*" {
		originAllowed := false
		for _, allowedOrig := range ps.corsConfig.AllowedOrigins {
			if allowedOrig == origin {
				allowedOrigin = origin
				originAllowed = true
				break
			}
		}
		if !originAllowed {
			return false
		}
	}

	// Handle preflight request
	if method == "OPTIONS" {
		var response strings.Builder
		response.WriteString("HTTP/1.1 200 OK\r\n")
		response.WriteString(fmt.Sprintf("Access-Control-Allow-Origin: %s\r\n", allowedOrigin))
		response.WriteString(fmt.Sprintf("Access-Control-Allow-Methods: %s\r\n", strings.Join(ps.corsConfig.AllowedMethods, ", ")))
		response.WriteString(fmt.Sprintf("Access-Control-Allow-Headers: %s\r\n", strings.Join(ps.corsConfig.AllowedHeaders, ", ")))
		if ps.corsConfig.AllowCredentials {
			response.WriteString("Access-Control-Allow-Credentials: true\r\n")
		}
		response.WriteString(fmt.Sprintf("Access-Control-Max-Age: %d\r\n", ps.corsConfig.MaxAge))
		response.WriteString("Content-Length: 0\r\n")
		response.WriteString("\r\n")

		c.Write([]byte(response.String()))
		return true
	}

	return false
}

func (ps *ProxyServer) forwardRequest(req *http.Request, upstream *loadbalancer.Upstream) (*http.Response, error) {
	// Create new request URL with upstream host
	targetURL := upstream.URL.ResolveReference(req.URL)

	// Create new request
	newReq, err := http.NewRequest(req.Method, targetURL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create new request: %w", err)
	}

	// Copy headers
	for name, values := range req.Header {
		for _, value := range values {
			newReq.Header.Add(name, value)
		}
	}

	// Add proxy headers
	newReq.Header.Set("X-Forwarded-For", req.RemoteAddr)
	newReq.Header.Set("X-Forwarded-Proto", "http")
	newReq.Header.Set("X-Forwarded-Host", req.Host)

	// Execute request
	return ps.client.Do(newReq)
}

func (ps *ProxyServer) sendResponse(c gnet.Conn, resp *http.Response) error {
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Build HTTP response
	var response strings.Builder
	response.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, resp.Status))

	// Add CORS headers if enabled
	if ps.corsConfig.Enabled {
		response.WriteString("Access-Control-Allow-Origin: *\r\n")
		if len(ps.corsConfig.ExposedHeaders) > 0 {
			response.WriteString(fmt.Sprintf("Access-Control-Expose-Headers: %s\r\n", strings.Join(ps.corsConfig.ExposedHeaders, ", ")))
		}
		if ps.corsConfig.AllowCredentials {
			response.WriteString("Access-Control-Allow-Credentials: true\r\n")
		}
	}

	// Copy headers
	for name, values := range resp.Header {
		for _, value := range values {
			response.WriteString(fmt.Sprintf("%s: %s\r\n", name, value))
		}
	}

	// Add content length if not present
	if resp.Header.Get("Content-Length") == "" {
		response.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	}

	response.WriteString("\r\n")
	response.WriteString(string(body))

	_, err = c.Write([]byte(response.String()))
	return err
}

func (ps *ProxyServer) sendErrorResponse(c gnet.Conn, statusCode int, message string) {
	response := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		statusCode, message, len(message), message)
	_, _ = c.Write([]byte(response))
}
