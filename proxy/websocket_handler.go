package proxy

import (
	"net/http"

	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

// WebSocketHandler handles WebSocket proxy requests
type WebSocketHandler struct {
	websocketProxy *WebSocketProxy
	logger         *zap.Logger
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(wsLB *loadbalancer.LoadBalancer, logger *zap.Logger, proxyConfig config.ProxyConfig) *WebSocketHandler {
	var wsProxy *WebSocketProxy
	if wsLB != nil {
		// Use the same load balancer for both parameters since we only have one
		wsProxy = NewWebSocketProxy(wsLB, wsLB, logger, proxyConfig)
	}

	return &WebSocketHandler{
		websocketProxy: wsProxy,
		logger:         logger,
	}
}

// IsWebSocketRequest checks if the HTTP request is a WebSocket upgrade request
func (wh *WebSocketHandler) IsWebSocketRequest(r *http.Request) bool {
	if wh.websocketProxy == nil {
		return false
	}
	
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	
	return wh.websocketProxy.IsWebSocketRequest(headers)
}

// IsWebSocketRequestFromHeaders checks if the request headers indicate a WebSocket upgrade
func (wh *WebSocketHandler) IsWebSocketRequestFromHeaders(headers map[string]string) bool {
	if wh.websocketProxy == nil {
		return false
	}
	
	return wh.websocketProxy.IsWebSocketRequest(headers)
}

// HandleWebSocketHTTP handles WebSocket connections through standard HTTP server
func (wh *WebSocketHandler) HandleWebSocketHTTP(w http.ResponseWriter, r *http.Request) {
	if wh.websocketProxy == nil {
		http.Error(w, "WebSocket proxy not initialized", http.StatusInternalServerError)
		return
	}
	
	err := wh.websocketProxy.HandleWebSocket(w, r)
	if err != nil {
		wh.logger.Error("WebSocket proxy error", zap.Error(err))
		// Don't write error response here as HandleWebSocket may have already written to the connection
	}
}

// IsEnabled returns true if WebSocket proxy is enabled
func (wh *WebSocketHandler) IsEnabled() bool {
	return wh.websocketProxy != nil
}