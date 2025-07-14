package loadbalancer

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"surikiti/config"
)

type Upstream struct {
	Name        string
	URL         *url.URL
	Weight      int
	HealthCheck string
	Healthy     int64 // atomic boolean (0 = unhealthy, 1 = healthy)
	Connections int64 // atomic counter for active connections
}

type LoadBalancer struct {
	upstreams []*Upstream
	method    string
	current   uint64 // for round robin
	mu        sync.RWMutex
	timeout   time.Duration
	retries   int
	healthTicker *time.Ticker
	shutdownChan chan struct{}
}

func NewLoadBalancer(upstreamConfigs []config.UpstreamConfig, lbConfig config.LoadBalancerConfig) (*LoadBalancer, error) {
	upstreams := make([]*Upstream, 0, len(upstreamConfigs))

	for _, uc := range upstreamConfigs {
		parsedURL, err := url.Parse(uc.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream URL %s: %w", uc.URL, err)
		}

		upstream := &Upstream{
			Name:        uc.Name,
			URL:         parsedURL,
			Weight:      uc.Weight,
			HealthCheck: uc.HealthCheck,
			Healthy:     1, // assume healthy initially
		}
		upstreams = append(upstreams, upstream)
	}

	return &LoadBalancer{
		upstreams: upstreams,
		method:    lbConfig.Method,
		timeout:   lbConfig.Timeout,
		retries:   lbConfig.MaxRetries,
	}, nil
}

// NewWebSocketLoadBalancer creates a new load balancer specifically for WebSocket upstreams
func NewWebSocketLoadBalancer(wsUpstreamConfigs []config.UpstreamConfig, lbConfig config.LoadBalancerConfig) (*LoadBalancer, error) {
	upstreams := make([]*Upstream, 0, len(wsUpstreamConfigs))

	for _, uc := range wsUpstreamConfigs {
		parsedURL, err := url.Parse(uc.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid WebSocket upstream URL %s: %w", uc.URL, err)
		}

		upstream := &Upstream{
			Name:        uc.Name,
			URL:         parsedURL,
			Weight:      uc.Weight,
			HealthCheck: uc.HealthCheck,
			Healthy:     1, // assume healthy initially
		}
		upstreams = append(upstreams, upstream)
	}

	return &LoadBalancer{
		upstreams: upstreams,
		method:    lbConfig.Method,
		timeout:   lbConfig.Timeout,
		retries:   lbConfig.MaxRetries,
	}, nil
}

func (lb *LoadBalancer) GetUpstream() *Upstream {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	healthyUpstreams := make([]*Upstream, 0)
	for _, upstream := range lb.upstreams {
		if atomic.LoadInt64(&upstream.Healthy) == 1 {
			healthyUpstreams = append(healthyUpstreams, upstream)
		}
	}

	if len(healthyUpstreams) == 0 {
		return nil
	}

	switch lb.method {
	case "round_robin":
		return lb.roundRobin(healthyUpstreams)
	case "weighted_round_robin":
		return lb.weightedRoundRobin(healthyUpstreams)
	case "least_connections":
		return lb.leastConnections(healthyUpstreams)
	case "single":
		return lb.single(healthyUpstreams)
	default:
		return lb.roundRobin(healthyUpstreams)
	}
}

// GetUpstreamByName returns a specific upstream by name if it's healthy
func (lb *LoadBalancer) GetUpstreamByName(name string) *Upstream {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	for _, upstream := range lb.upstreams {
		if upstream.Name == name && atomic.LoadInt64(&upstream.Healthy) == 1 {
			return upstream
		}
	}
	return nil
}

func (lb *LoadBalancer) roundRobin(upstreams []*Upstream) *Upstream {
	index := atomic.AddUint64(&lb.current, 1) % uint64(len(upstreams))
	return upstreams[index]
}

func (lb *LoadBalancer) weightedRoundRobin(upstreams []*Upstream) *Upstream {
	totalWeight := 0
	for _, upstream := range upstreams {
		totalWeight += upstream.Weight
	}

	if totalWeight == 0 {
		return lb.roundRobin(upstreams)
	}

	index := atomic.AddUint64(&lb.current, 1) % uint64(totalWeight)
	currentWeight := uint64(0)

	for _, upstream := range upstreams {
		currentWeight += uint64(upstream.Weight)
		if index < currentWeight {
			return upstream
		}
	}

	return upstreams[0]
}

func (lb *LoadBalancer) leastConnections(upstreams []*Upstream) *Upstream {
	var selected *Upstream
	minConnections := int64(-1)

	for _, upstream := range upstreams {
		connections := atomic.LoadInt64(&upstream.Connections)
		if minConnections == -1 || connections < minConnections {
			minConnections = connections
			selected = upstream
		}
	}

	return selected
}

func (lb *LoadBalancer) single(upstreams []*Upstream) *Upstream {
	// Always return the first healthy upstream (single mode)
	if len(upstreams) > 0 {
		return upstreams[0]
	}
	return nil
}

func (lb *LoadBalancer) IncreaseConnections(upstream *Upstream) {
	atomic.AddInt64(&upstream.Connections, 1)
}

func (lb *LoadBalancer) DecreaseConnections(upstream *Upstream) {
	atomic.AddInt64(&upstream.Connections, -1)
}

func (lb *LoadBalancer) MarkUnhealthy(upstream *Upstream) {
	atomic.StoreInt64(&upstream.Healthy, 0)
}

func (lb *LoadBalancer) MarkHealthy(upstream *Upstream) {
	atomic.StoreInt64(&upstream.Healthy, 1)
}

func (lb *LoadBalancer) StartHealthCheck() {
	lb.healthTicker = time.NewTicker(30 * time.Second)
	lb.shutdownChan = make(chan struct{})
	go func() {
		for {
			select {
			case <-lb.healthTicker.C:
				lb.performHealthCheck()
			case <-lb.shutdownChan:
				return
			}
		}
	}()
}

func (lb *LoadBalancer) StopHealthCheck() {
	if lb.healthTicker != nil {
		lb.healthTicker.Stop()
	}
	if lb.shutdownChan != nil {
		close(lb.shutdownChan)
	}
}

func (lb *LoadBalancer) performHealthCheck() {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, upstream := range lb.upstreams {
		go func(u *Upstream) {
			// Skip health check for WebSocket upstreams or assume they're healthy
			if u.URL.Scheme == "ws" || u.URL.Scheme == "wss" {
				// For WebSocket upstreams, we assume they're healthy
				// In a production environment, you might want to implement
				// a WebSocket-specific health check
				lb.MarkHealthy(u)
				return
			}
			
			healthURL := u.URL.String() + u.HealthCheck
			resp, err := client.Get(healthURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				lb.MarkUnhealthy(u)
			} else {
				lb.MarkHealthy(u)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}(upstream)
	}
}