package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
	"go.uber.org/zap"

	"surikiti/config"
	"surikiti/loadbalancer"
)

type HTTP2HTTP3Server struct {
	loadBalancer *loadbalancer.LoadBalancer
	logger       *zap.Logger
	config       config.ProxyConfig
	http2Server  *http.Server
	http3Server  *http3.Server
	tlsConfig    *tls.Config
}

func NewHTTP2HTTP3Server(lb *loadbalancer.LoadBalancer, logger *zap.Logger, cfg config.ProxyConfig) *HTTP2HTTP3Server {
	server := &HTTP2HTTP3Server{
		loadBalancer: lb,
		logger:       logger,
		config:       cfg,
	}

	// Setup TLS config if certificates are provided
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			logger.Error("Failed to load TLS certificates", zap.Error(err))
			return server
		}

		server.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"}, // HTTP/2 and HTTP/1.1
		}

		// Add HTTP/3 support if enabled
		if cfg.EnableHTTP3 {
			server.tlsConfig.NextProtos = append([]string{"h3"}, server.tlsConfig.NextProtos...)
		}
	}

	return server
}

func (h *HTTP2HTTP3Server) StartHTTP2Server(addr string) error {
	if !h.config.EnableHTTP2 || h.tlsConfig == nil {
		return fmt.Errorf("HTTP/2 not enabled or TLS not configured")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleHTTP2Request)

	h.http2Server = &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: h.tlsConfig,
		ReadTimeout:  h.config.RequestTimeout,
		WriteTimeout: h.config.ResponseTimeout,
		IdleTimeout:  h.config.KeepAliveTimeout,
	}

	// Configure HTTP/2
	if err := http2.ConfigureServer(h.http2Server, &http2.Server{
		MaxConcurrentStreams: uint32(h.config.MaxConnections),
		MaxReadFrameSize:     uint32(h.config.BufferSize),
		IdleTimeout:          h.config.KeepAliveTimeout,
	}); err != nil {
		return fmt.Errorf("failed to configure HTTP/2: %w", err)
	}

	h.logger.Info("Starting HTTP/2 server", zap.String("addr", addr))
	return h.http2Server.ListenAndServeTLS("", "")
}

func (h *HTTP2HTTP3Server) StartHTTP3Server() error {
	if !h.config.EnableHTTP3 || h.tlsConfig == nil {
		return fmt.Errorf("HTTP/3 not enabled or TLS not configured")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleHTTP3Request)

	addr := fmt.Sprintf(":%d", h.config.HTTP3Port)

	h.http3Server = &http3.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: h.tlsConfig,
		QUICConfig: &quic.Config{
			MaxIdleTimeout:  h.config.KeepAliveTimeout,
			KeepAlivePeriod: h.config.KeepAliveTimeout / 2,
		},
	}

	h.logger.Info("Starting HTTP/3 server", zap.String("addr", addr))
	return h.http3Server.ListenAndServe()
}

func (h *HTTP2HTTP3Server) Shutdown(ctx context.Context) error {
	var err error
	
	if h.http2Server != nil {
		h.logger.Info("Shutting down HTTP/2 server")
		if shutdownErr := h.http2Server.Shutdown(ctx); shutdownErr != nil {
			h.logger.Error("Error shutting down HTTP/2 server", zap.Error(shutdownErr))
			err = shutdownErr
		}
	}
	
	if h.http3Server != nil {
		h.logger.Info("Shutting down HTTP/3 server")
		if shutdownErr := h.http3Server.Close(); shutdownErr != nil {
			h.logger.Error("Error shutting down HTTP/3 server", zap.Error(shutdownErr))
			err = shutdownErr
		}
	}
	
	return err
}

func (h *HTTP2HTTP3Server) handleHTTP2Request(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("HTTP/2 request received", 
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("proto", r.Proto))

	h.proxyRequest(w, r, "HTTP/2")
}

func (h *HTTP2HTTP3Server) handleHTTP3Request(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("HTTP/3 request received", 
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("proto", r.Proto))

	h.proxyRequest(w, r, "HTTP/3")
}

func (h *HTTP2HTTP3Server) proxyRequest(w http.ResponseWriter, r *http.Request, protocol string) {
	// Get upstream server
	upstream := h.loadBalancer.GetUpstream()
	if upstream == nil {
		h.logger.Error("No healthy upstream available", zap.String("protocol", protocol))
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Increment connection count
	h.loadBalancer.IncreaseConnections(upstream)
	defer h.loadBalancer.DecreaseConnections(upstream)

	// Create HTTP client with appropriate configuration
	client := &http.Client{
		Timeout: h.config.RequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        h.config.MaxIdleConns,
			MaxIdleConnsPerHost: h.config.MaxIdleConnsPerHost,
			MaxConnsPerHost:     h.config.MaxConnsPerHost,
			IdleConnTimeout:     h.config.IdleConnTimeout,
			DialContext: (&net.Dialer{
				Timeout:   h.config.RequestTimeout,
				KeepAlive: h.config.KeepAliveTimeout,
			}).DialContext,
			TLSHandshakeTimeout: h.config.RequestTimeout,
		},
	}

	// Configure HTTP/2 support for upstream if enabled
	if h.config.EnableHTTP2 {
		if err := http2.ConfigureTransport(client.Transport.(*http.Transport)); err != nil {
			h.logger.Warn("Failed to configure HTTP/2 transport", zap.Error(err))
		}
	}

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
	upstreamReq.Header.Set("X-Forwarded-Proto", protocol)
	upstreamReq.Header.Set("X-Forwarded-Host", r.Host)

	// Make request to upstream
	ctx, cancel := context.WithTimeout(r.Context(), h.config.RequestTimeout)
	defer cancel()
	upstreamReq = upstreamReq.WithContext(ctx)

	resp, err := client.Do(upstreamReq)
	if err != nil {
		h.logger.Error("Failed to proxy request to upstream", 
			zap.Error(err),
			zap.String("upstream", upstream.URL.String()),
			zap.String("protocol", protocol))
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Add server header
	w.Header().Set("Server", "Surikiti-Proxy/1.0")
	w.Header().Set("X-Proxy-Protocol", protocol)

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		h.logger.Error("Failed to copy response body", 
			zap.Error(err),
			zap.String("protocol", protocol))
	}

	h.logger.Debug("Request proxied successfully", 
		zap.String("protocol", protocol),
		zap.String("upstream", upstream.URL.String()),
		zap.Int("status", resp.StatusCode))
}