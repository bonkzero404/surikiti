package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"

	"surikiti/config"
	"surikiti/loadbalancer"
)

// HTTPHandler handles HTTP proxy requests
type HTTPHandler struct {
	loadBalancer *loadbalancer.LoadBalancer
	client       *fasthttp.Client
	httpClient   *http.Client
	logger       *zap.Logger
	proxyConfig  config.ProxyConfig
	corsConfig   config.CORSConfig
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(lb *loadbalancer.LoadBalancer, client *fasthttp.Client, httpClient *http.Client, logger *zap.Logger, proxyConfig config.ProxyConfig, corsConfig config.CORSConfig) *HTTPHandler {
	return &HTTPHandler{
		loadBalancer: lb,
		client:       client,
		httpClient:   httpClient,
		logger:       logger,
		proxyConfig:  proxyConfig,
		corsConfig:   corsConfig,
	}
}

// HandleHTTPProxy handles regular HTTP proxy requests using standard HTTP server
func (h *HTTPHandler) HandleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	// Get upstream server
	upstream := h.loadBalancer.GetUpstream()
	if upstream == nil {
		h.logger.Error("No healthy upstream available")
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Increment connection count
	h.loadBalancer.IncreaseConnections(upstream)
	defer h.loadBalancer.DecreaseConnections(upstream)

	// Use the reusable HTTP client
	client := h.httpClient

	// Create upstream request
	upstreamURL := upstream.URL.String() + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		h.logger.Error("Failed to create upstream request", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for name, values := range r.Header {
		for _, value := range values {
			upstreamReq.Header.Add(name, value)
		}
	}

	// Add forwarding headers
	upstreamReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
	upstreamReq.Header.Set("X-Forwarded-Proto", "http")
	upstreamReq.Header.Set("X-Forwarded-Host", r.Host)

	// Make request to upstream with retry logic
	ctx, cancel := context.WithTimeout(r.Context(), h.proxyConfig.RequestTimeout*2)
	defer cancel()
	upstreamReq = upstreamReq.WithContext(ctx)

	var resp *http.Response
	maxRetries := 3
	
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = client.Do(upstreamReq)
		if err == nil {
			break
		}
		
		// Log retry attempt
		if attempt < maxRetries {
			h.logger.Warn("Retrying request to upstream", 
				zap.Error(err),
				zap.String("upstream", upstream.URL.String()),
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", maxRetries))
			
			// Brief delay before retry
			time.Sleep(time.Millisecond * 100 * time.Duration(attempt+1))
			
			// Create new request for retry (body might be consumed)
			if r.Body != nil {
				r.Body.Close()
			}
			upstreamReq, _ = http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
			// Copy headers again
			for name, values := range r.Header {
				for _, value := range values {
					upstreamReq.Header.Add(name, value)
				}
			}
			// Add forwarding headers again
			upstreamReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
			upstreamReq.Header.Set("X-Forwarded-Proto", "http")
			upstreamReq.Header.Set("X-Forwarded-Host", r.Host)
		}
	}
	
	if err != nil {
		h.logger.Error("Failed to proxy request to upstream after retries", 
			zap.Error(err),
			zap.String("upstream", upstream.URL.String()),
			zap.Int("attempts", maxRetries+1))
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Add CORS headers if enabled
	if h.corsConfig.Enabled {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if len(h.corsConfig.ExposedHeaders) > 0 {
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(h.corsConfig.ExposedHeaders, ", "))
		}
		if h.corsConfig.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
	}

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Add server header
	w.Header().Set("Server", "Surikiti-Proxy/1.0")
	w.Header().Set("X-Proxy-Protocol", "HTTP/1.1")

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		h.logger.Error("Failed to copy response body", zap.Error(err))
	}

	h.logger.Debug("Request proxied successfully", 
		zap.String("upstream", upstream.URL.String()),
		zap.Int("status", resp.StatusCode))
}

// HandleTraffic handles gnet traffic for HTTP requests
func (h *HTTPHandler) HandleTraffic(c gnet.Conn, reqData []byte) gnet.Action {
	// Check for empty request data
	if len(reqData) == 0 {
		h.logger.Debug("Received empty request data")
		return gnet.Close
	}

	// Check max body size first
	if int64(len(reqData)) > h.proxyConfig.MaxBodySize {
		h.logger.Warn("Request too large", zap.Int("size", len(reqData)), zap.Int64("max", h.proxyConfig.MaxBodySize))
		h.sendErrorResponse(c, fasthttp.StatusRequestEntityTooLarge, "Request Entity Too Large")
		return gnet.None
	}

	// Parse HTTP request using fasthttp properly
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	bufReader := bufio.NewReader(bytes.NewReader(reqData))
	if readErr := req.Read(bufReader); readErr != nil {
		h.logger.Debug("Failed to parse HTTP request", zap.Error(readErr))
		h.sendErrorResponse(c, fasthttp.StatusBadRequest, "Bad Request")
		return gnet.None
	}

	// Validate HTTP method
	method := string(req.Header.Method())
	if method == "" {
		h.logger.Debug("Missing HTTP method in request")
		h.sendErrorResponse(c, fasthttp.StatusBadRequest, "Bad Request")
		return gnet.None
	}

	// Handle CORS preflight requests
	if h.handleCORS(req, c) {
		return gnet.None
	}

	// Get upstream server
	upstream := h.loadBalancer.GetUpstream()
	if upstream == nil {
		h.sendErrorResponse(c, fasthttp.StatusServiceUnavailable, "Service Unavailable")
		return gnet.None
	}

	// Increment connection count
	h.loadBalancer.IncreaseConnections(upstream)
	defer h.loadBalancer.DecreaseConnections(upstream)

	// Forward request to upstream
	resp, err := h.forwardRequest(req, upstream)
	if err != nil {
		h.sendErrorResponse(c, fasthttp.StatusBadGateway, "Bad Gateway")
		return gnet.None
	}
	defer fasthttp.ReleaseResponse(resp)

	// Send response back to client using fasthttp response writer
	if err := h.sendResponse(c, resp); err != nil {
		return gnet.Close
	}

	return gnet.None
}

// handleCORS adds CORS headers to the response if CORS is enabled
func (h *HTTPHandler) handleCORS(req *fasthttp.Request, c gnet.Conn) bool {
	if !h.corsConfig.Enabled {
		return false
	}

	origin := string(req.Header.Peek("Origin"))
	method := string(req.Header.Method())

	// Check if origin is allowed
	allowedOrigin := "*"
	if len(h.corsConfig.AllowedOrigins) > 0 && h.corsConfig.AllowedOrigins[0] != "*" {
		originAllowed := false
		if slices.Contains(h.corsConfig.AllowedOrigins, origin) {
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
		resp.Header.Set("Access-Control-Allow-Methods", strings.Join(h.corsConfig.AllowedMethods, ", "))
		resp.Header.Set("Access-Control-Allow-Headers", strings.Join(h.corsConfig.AllowedHeaders, ", "))
		if h.corsConfig.AllowCredentials {
			resp.Header.Set("Access-Control-Allow-Credentials", "true")
		}
		resp.Header.Set("Access-Control-Max-Age", strconv.Itoa(h.corsConfig.MaxAge))
		resp.Header.Set("Content-Length", "0")

		// Write response using fasthttp
		h.writeResponse(c, resp)
		return true
	}

	return false
}

func (h *HTTPHandler) forwardRequest(req *fasthttp.Request, upstream *loadbalancer.Upstream) (*fasthttp.Response, error) {
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
		err = h.client.Do(req, fastResp)
		if err == nil {
			return fastResp, nil
		}

		// Mark upstream as unhealthy on persistent errors
		if i == maxRetries-1 {
			h.loadBalancer.MarkUnhealthy(upstream)
		}

		// Minimal delay before retry
		time.Sleep(time.Millisecond * 10)
	}

	fasthttp.ReleaseResponse(fastResp)
	return nil, fmt.Errorf("failed to execute request after %d retries: %w", maxRetries, err)
}

func (h *HTTPHandler) sendResponse(c gnet.Conn, resp *fasthttp.Response) error {
	// Add CORS headers if enabled
	if h.corsConfig.Enabled {
		resp.Header.Set("Access-Control-Allow-Origin", "*")
		if len(h.corsConfig.ExposedHeaders) > 0 {
			resp.Header.Set("Access-Control-Expose-Headers", strings.Join(h.corsConfig.ExposedHeaders, ", "))
		}
		if h.corsConfig.AllowCredentials {
			resp.Header.Set("Access-Control-Allow-Credentials", "true")
		}
	}

	return h.writeResponse(c, resp)
}

// writeResponse efficiently writes fasthttp response to gnet connection
func (h *HTTPHandler) writeResponse(c gnet.Conn, resp *fasthttp.Response) error {
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

func (h *HTTPHandler) sendErrorResponse(c gnet.Conn, statusCode int, message string) {
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	resp.SetStatusCode(statusCode)
	resp.Header.Set("Content-Type", "text/plain")
	resp.SetBodyString(message)

	h.writeResponse(c, resp)
}