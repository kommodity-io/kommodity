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
//
// Tunnel establishment (which involves blocking network I/O) is performed
// outside the pool mutex. The establishing map coordinates concurrent callers
// for the same cluster: only one goroutine establishes at a time; others wait
// on the channel and retry once the establishing goroutine is done.
type TunnelPool struct {
	mu           sync.Mutex
	tunnels      map[string]*Tunnel
	idleTimers   map[string]*time.Timer
	establishing map[string]chan struct{}
	config       *config.TalosProxyConfig
	client       client.Client
	logger       *zap.Logger
}

// NewTunnelPool creates a new tunnel pool.
func NewTunnelPool(
	cfg *config.TalosProxyConfig,
	kubeClient client.Client,
	logger *zap.Logger,
) *TunnelPool {
	return &TunnelPool{
		tunnels:      make(map[string]*Tunnel),
		idleTimers:   make(map[string]*time.Timer),
		establishing: make(map[string]chan struct{}),
		config:       cfg,
		client:       kubeClient,
		logger:       logger,
	}
}

// GetOrCreateTunnel retrieves an existing tunnel or creates and establishes a new one.
// The pool mutex is not held during tunnel establishment (which performs blocking
// network I/O), so concurrent requests for other clusters are never blocked.
// Concurrent requests for the same cluster wait for the single in-progress
// establishment to complete, then retry.
func (p *TunnelPool) GetOrCreateTunnel(
	ctx context.Context,
	clusterName string,
	namespace string,
) (*Tunnel, error) {
	for {
		p.mu.Lock()

		// Fast path: existing open tunnel.
		tunnel, exists := p.tunnels[clusterName]
		if exists && !tunnel.IsClosed() {
			p.cancelIdleTimerLocked(clusterName)
			p.mu.Unlock()

			return tunnel, nil
		}

		// Clean up stale closed tunnel.
		if exists {
			p.cancelIdleTimerLocked(clusterName)

			_ = tunnel.Close()

			delete(p.tunnels, clusterName)
		}

		// Wait path: another goroutine is already establishing this tunnel.
		// Release the lock and block until it signals completion, then retry.
		if ch, inProgress := p.establishing[clusterName]; inProgress {
			p.mu.Unlock()

			select {
			case <-ch:
				// Establishing goroutine finished (success or failure); retry.
				continue
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled waiting for tunnel for cluster %s: %w",
					clusterName, ctx.Err())
			}
		}

		// Create path: register ourselves as the establishing goroutine.
		logger := logging.FromContext(ctx)
		logger.Info("Creating new tunnel",
			zap.String("cluster", clusterName),
			zap.String("namespace", namespace))

		ch := make(chan struct{})
		p.establishing[clusterName] = ch

		newTunnel := NewTunnel(TunnelDeps{
			ClusterName: clusterName,
			Config:      p.config,
			OnIdle:      func() { p.scheduleIdleClose(clusterName) },
		})

		p.mu.Unlock()

		// Establish without holding the pool mutex.
		err := newTunnel.Establish(ctx, p.client)

		p.mu.Lock()

		delete(p.establishing, clusterName)
		close(ch) // wake up all waiters regardless of success or failure

		if err != nil {
			p.mu.Unlock()

			return nil, fmt.Errorf("failed to establish tunnel for cluster %s: %w", clusterName, err)
		}

		p.tunnels[clusterName] = newTunnel

		p.mu.Unlock()

		return newTunnel, nil
	}
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
