package proxy

import (
	"bufio"
	"bytes"
	"fmt"
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
}

func NewProxyServer(lb *loadbalancer.LoadBalancer, logger *zap.Logger, proxyConfig config.ProxyConfig, corsConfig config.CORSConfig) *ProxyServer {
	// Create fasthttp client optimized for high concurrency
	client := &fasthttp.Client{
		ReadTimeout:                   time.Second * 2, // Reduced timeout
		WriteTimeout:                  time.Second * 2, // Reduced timeout
		MaxIdleConnDuration:           time.Second * 10, // Increased for connection reuse
		MaxConnDuration:               time.Minute * 2, // Longer duration for efficiency
		MaxConnsPerHost:               100, // Increased for high concurrency
		DisableHeaderNamesNormalizing: false,
		DisablePathNormalizing:        false,
		RetryIf: func(request *fasthttp.Request) bool {
			// Retry on connection errors
			return true
		},
		Dial: (&fasthttp.TCPDialer{
			Concurrency:      1000, // High concurrency for load testing
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

	// Start health check
	lb.StartHealthCheck()

	return ps
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
		return gnet.Close
	}

	// Parse HTTP request using fasthttp properly
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	// Check max body size first
	if int64(len(reqData)) > ps.proxyConfig.MaxBodySize {
		ps.sendErrorResponse(c, fasthttp.StatusRequestEntityTooLarge, "Request Entity Too Large")
		return gnet.None
	}

	bufReader := bufio.NewReader(bytes.NewReader(reqData))
	if err := req.Read(bufReader); err != nil {
		ps.sendErrorResponse(c, fasthttp.StatusBadRequest, "Bad Request")
		return gnet.None
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
