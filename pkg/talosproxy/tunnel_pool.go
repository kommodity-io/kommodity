package talosproxy

import (
	"context"
	"fmt"
	"sync"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TunnelPool manages port-forward tunnels keyed by cluster name.
type TunnelPool struct {
	mu      sync.RWMutex
	tunnels map[string]*Tunnel
	config  *config.TalosProxyConfig
	client  client.Client
}

// NewTunnelPool creates a new tunnel pool.
func NewTunnelPool(config *config.TalosProxyConfig, kubeClient client.Client) *TunnelPool {
	return &TunnelPool{
		tunnels: make(map[string]*Tunnel),
		config:  config,
		client:  kubeClient,
	}
}

// GetOrCreateTunnel retrieves an existing tunnel or creates and establishes a new one.
func (p *TunnelPool) GetOrCreateTunnel(
	ctx context.Context,
	clusterName string,
	namespace string,
) (*Tunnel, error) {
	p.mu.RLock()
	tunnel, exists := p.tunnels[clusterName]
	p.mu.RUnlock()

	if exists && !tunnel.IsClosed() {
		return tunnel, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	tunnel, exists = p.tunnels[clusterName]
	if exists && !tunnel.IsClosed() {
		return tunnel, nil
	}

	// Remove stale closed tunnel if present
	if exists {
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

	for _, tunnel := range p.tunnels {
		_ = tunnel.Close()
	}

	p.tunnels = make(map[string]*Tunnel)
}
