package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"surikiti/loadbalancer"

	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

type ProxyServer struct {
	loadBalancer *loadbalancer.LoadBalancer
	logger       *zap.Logger
	client       *http.Client
}

func NewProxyServer(lb *loadbalancer.LoadBalancer, logger *zap.Logger, timeout time.Duration) *ProxyServer {
	return &ProxyServer{
		loadBalancer: lb,
		logger:       logger,
		client: &http.Client{
			Timeout: timeout,
		},
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
		// EOF is normal when client closes connection gracefully
		if err.Error() == "EOF" || strings.Contains(err.Error(), "read: EOF") {
			ps.logger.Debug("Connection closed by client", zap.String("remote", c.RemoteAddr().String()))
		} else {
			ps.logger.Error("Connection closed with error", zap.Error(err))
		}
	} else {
		ps.logger.Debug("Connection closed", zap.String("remote", c.RemoteAddr().String()))
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
		ps.sendErrorResponse(c, http.StatusBadRequest, "Bad Request")
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
	reader := bytes.NewReader(data)
	bufReader := bufio.NewReader(reader)
	return http.ReadRequest(bufReader)
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