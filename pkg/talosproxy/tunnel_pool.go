package talosproxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TunnelPool manages port-forward tunnels keyed by cluster name.
//
// Lock ordering: pool.mu is always acquired before tunnel.mu.
// The onIdle callback (which acquires pool.mu) is called from
// ReleaseConn without holding tunnel.mu, preserving this order.
type TunnelPool struct {
	mu         sync.Mutex
	tunnels    map[string]*Tunnel
	idleTimers map[string]*time.Timer
	config     *config.TalosProxyConfig
	client     client.Client
	logger     *zap.Logger
}

// NewTunnelPool creates a new tunnel pool.
func NewTunnelPool(
	cfg *config.TalosProxyConfig,
	kubeClient client.Client,
	logger *zap.Logger,
) *TunnelPool {
	return &TunnelPool{
		tunnels:    make(map[string]*Tunnel),
		idleTimers: make(map[string]*time.Timer),
		config:     cfg,
		client:     kubeClient,
		logger:     logger,
	}
}

// GetOrCreateTunnel retrieves an existing tunnel or creates and establishes a new one.
func (p *TunnelPool) GetOrCreateTunnel(
	ctx context.Context,
	clusterName string,
	namespace string,
) (*Tunnel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	tunnel, exists := p.tunnels[clusterName]
	if exists && !tunnel.IsClosed() {
		p.cancelIdleTimerLocked(clusterName)

		return tunnel, nil
	}

	// Remove stale closed tunnel if present
	if exists {
		p.cancelIdleTimerLocked(clusterName)

		_ = tunnel.Close()

		delete(p.tunnels, clusterName)
	}

	logger := logging.FromContext(ctx)
	logger.Info("Creating new tunnel",
		zap.String("cluster", clusterName),
		zap.String("namespace", namespace))

	tunnel = NewTunnel(TunnelDeps{
		ClusterName: clusterName,
		Config:      p.config,
		OnIdle:      func() { p.scheduleIdleClose(clusterName) },
	})

	err := tunnel.Establish(ctx, p.client)
	if err != nil {
		return nil, fmt.Errorf("failed to establish tunnel for cluster %s: %w", clusterName, err)
	}

	p.tunnels[clusterName] = tunnel

	return tunnel, nil
}

// RemoveTunnel closes and removes the tunnel for the given cluster.
func (p *TunnelPool) RemoveTunnel(clusterName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cancelIdleTimerLocked(clusterName)

	tunnel, exists := p.tunnels[clusterName]
	if !exists {
		return
	}

	_ = tunnel.Close()

	delete(p.tunnels, clusterName)
}

// CloseAll closes all tunnels in the pool.
func (p *TunnelPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, timer := range p.idleTimers {
		timer.Stop()
	}

	p.idleTimers = make(map[string]*time.Timer)

	for _, tunnel := range p.tunnels {
		_ = tunnel.Close()
	}

	p.tunnels = make(map[string]*Tunnel)
}

// scheduleIdleClose starts an idle timer for the given cluster. When the timer
// fires, the tunnel is closed and removed if it still has zero active connections.
func (p *TunnelPool) scheduleIdleClose(clusterName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cancelIdleTimerLocked(clusterName)

	p.idleTimers[clusterName] = time.AfterFunc(p.config.IdleTimeout, func() {
		p.closeIdleTunnel(clusterName)
	})
}

// closeIdleTunnel closes and removes a tunnel if it still exists and has zero
// active connections.
func (p *TunnelPool) closeIdleTunnel(clusterName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.idleTimers, clusterName)

	tunnel, exists := p.tunnels[clusterName]
	if !exists {
		return
	}

	if tunnel.ActiveConns() > 0 {
		return
	}

	_ = tunnel.Close()

	delete(p.tunnels, clusterName)

	p.logger.Info("Closed idle tunnel",
		zap.String("cluster", clusterName))
}

// cancelIdleTimerLocked stops and removes the idle timer for the given cluster.
// Must be called while holding p.mu.
func (p *TunnelPool) cancelIdleTimerLocked(clusterName string) {
	timer, exists := p.idleTimers[clusterName]
	if !exists {
		return
	}

	timer.Stop()

	delete(p.idleTimers, clusterName)
}
