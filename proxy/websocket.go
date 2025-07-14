package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

type WebSocketProxy struct {
	loadBalancer   *loadbalancer.LoadBalancer
	wsLoadBalancer *loadbalancer.LoadBalancer
	logger         *zap.Logger
	config         config.ProxyConfig
	upgrader       websocket.Upgrader
}

func NewWebSocketProxy(lb *loadbalancer.LoadBalancer, wsLB *loadbalancer.LoadBalancer, logger *zap.Logger, cfg config.ProxyConfig) *WebSocketProxy {
	return &WebSocketProxy{
		loadBalancer:   lb,
		wsLoadBalancer: wsLB,
		logger:         logger,
		config:         cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.WebSocketBufferSize,
			WriteBufferSize: cfg.WebSocketBufferSize,
			CheckOrigin: func(r *http.Request) bool {
				// Allow all origins for now - should be configurable
				return true
			},
			HandshakeTimeout: cfg.WebSocketTimeout,
		},
	}
}

func (ws *WebSocketProxy) IsWebSocketRequest(headers map[string]string) bool {
	connection := strings.ToLower(headers["Connection"])
	upgrade := strings.ToLower(headers["Upgrade"])
	
	return strings.Contains(connection, "upgrade") && upgrade == "websocket"
}

func (ws *WebSocketProxy) HandleWebSocket(w http.ResponseWriter, r *http.Request) error {
	// Get WebSocket-specific upstream server from dedicated WebSocket load balancer
	upstream := ws.wsLoadBalancer.GetUpstream()
	if upstream == nil {
		ws.logger.Error("No healthy WebSocket upstream available")
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return nil
	}

	// Increment connection count
	ws.wsLoadBalancer.IncreaseConnections(upstream)
	defer ws.wsLoadBalancer.DecreaseConnections(upstream)

	// Parse upstream URL
	upstreamURL := upstream.URL
	if upstreamURL == nil {
		ws.logger.Error("Invalid upstream URL: nil")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return fmt.Errorf("invalid upstream URL: nil")
	}

	// Use WebSocket URL directly or convert from HTTP
	var upstreamWSURL *url.URL
	if upstreamURL.Scheme == "ws" || upstreamURL.Scheme == "wss" {
		// Use WebSocket URL directly
		upstreamWSURL = &url.URL{
			Scheme: upstreamURL.Scheme,
			Host:   upstreamURL.Host,
			Path:   r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
	} else {
		// Convert HTTP to WebSocket
		scheme := "ws"
		if upstreamURL.Scheme == "https" {
			scheme = "wss"
		}
		upstreamWSURL = &url.URL{
			Scheme: scheme,
			Host:   upstreamURL.Host,
			Path:   r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
	}

	// Upgrade client connection
	clientConn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		ws.logger.Error("Failed to upgrade client connection", zap.Error(err))
		return err
	}
	defer clientConn.Close()

	// Connect to upstream WebSocket
	upstreamConn, _, err := websocket.DefaultDialer.Dial(upstreamWSURL.String(), nil)
	if err != nil {
		ws.logger.Error("Failed to connect to upstream WebSocket", 
			zap.Error(err), 
			zap.String("upstream", upstreamWSURL.String()))
		clientConn.WriteMessage(websocket.CloseMessage, 
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Upstream connection failed"))
		return err
	}
	defer upstreamConn.Close()

	ws.logger.Info("WebSocket connection established", 
		zap.String("client", r.RemoteAddr),
		zap.String("upstream", upstreamWSURL.String()))

	// Set connection timeouts
	if ws.config.WebSocketTimeout > 0 {
		clientConn.SetReadDeadline(time.Now().Add(ws.config.WebSocketTimeout))
		upstreamConn.SetReadDeadline(time.Now().Add(ws.config.WebSocketTimeout))
	}

	// Start bidirectional proxying
	errorChan := make(chan error, 2)

	// Client to upstream
	go ws.proxyMessages(clientConn, upstreamConn, "client->upstream", errorChan)

	// Upstream to client
	go ws.proxyMessages(upstreamConn, clientConn, "upstream->client", errorChan)

	// Wait for either direction to close or error
	err = <-errorChan
	if err != nil {
		ws.logger.Debug("WebSocket connection closed", zap.Error(err))
	}

	return nil
}

func (ws *WebSocketProxy) proxyMessages(src, dst *websocket.Conn, direction string, errorChan chan error) {
	for {
		// Reset read deadline if configured
		if ws.config.WebSocketTimeout > 0 {
			src.SetReadDeadline(time.Now().Add(ws.config.WebSocketTimeout))
		}

		messageType, message, err := src.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				ws.logger.Error("WebSocket read error", 
					zap.Error(err), 
					zap.String("direction", direction))
			}
			errorChan <- err
			return
		}

		// Reset write deadline if configured
		if ws.config.WebSocketTimeout > 0 {
			dst.SetWriteDeadline(time.Now().Add(ws.config.WebSocketTimeout))
		}

		err = dst.WriteMessage(messageType, message)
		if err != nil {
			ws.logger.Error("WebSocket write error", 
				zap.Error(err), 
				zap.String("direction", direction))
			errorChan <- err
			return
		}

		ws.logger.Debug("WebSocket message proxied", 
			zap.String("direction", direction),
			zap.Int("messageType", messageType),
			zap.Int("size", len(message)))
	}
}