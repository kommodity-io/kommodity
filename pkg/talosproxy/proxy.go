package talosproxy

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProxyDeps holds the dependencies needed to create a Proxy.
type ProxyDeps struct {
	Config      *config.TalosProxyConfig
	Client      client.Client
	Interceptor Interceptor
}

// Proxy is the main proxy struct that manages the lifecycle of the Talos gRPC proxy.
// It implements manager.Runnable so it can be started and stopped with the controller manager.
type Proxy struct {
	config       *config.TalosProxyConfig
	cidrRegistry *CIDRRegistry
	tunnelPool   *TunnelPool
	interceptor  Interceptor
	listener     net.Listener
	mu           sync.Mutex
	stopOnce     sync.Once
}

// NewProxy creates a new Proxy instance.
func NewProxy(deps ProxyDeps) *Proxy {
	return &Proxy{
		config:       deps.Config,
		cidrRegistry: NewCIDRRegistry(),
		tunnelPool:   NewTunnelPool(deps.Config, deps.Client),
		interceptor:  deps.Interceptor,
	}
}

// Start begins accepting connections on the proxy listener.
// This implements the manager.Runnable interface.
func (p *Proxy) Start(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	if !p.config.Enabled {
		logger.Info("Talos proxy is disabled, not starting")

		return nil
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", p.config.ListenPort)

	listenConfig := net.ListenConfig{}

	listener, err := listenConfig.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	p.mu.Lock()
	p.listener = listener
	p.mu.Unlock()

	logger.Info("Talos proxy started",
		zap.String("listenAddr", listenAddr))

	go p.acceptLoop(ctx, listener)

	// Block until context is cancelled
	<-ctx.Done()

	logger.Info("Talos proxy shutting down")

	return p.cleanup()
}

// RegisterCluster registers a new cluster CIDR and updates interception rules.
func (p *Proxy) RegisterCluster(clusterName string, namespace string, cidr *net.IPNet) error {
	p.cidrRegistry.Register(clusterName, namespace, cidr)

	err := p.interceptor.UpdateRules(p.cidrRegistry.AllCIDRs())
	if err != nil {
		return fmt.Errorf("failed to update interception rules after registering cluster %s: %w",
			clusterName, err)
	}

	return nil
}

// DeregisterCluster removes a cluster and updates interception rules.
func (p *Proxy) DeregisterCluster(clusterName string) error {
	p.cidrRegistry.Deregister(clusterName)
	p.tunnelPool.RemoveTunnel(clusterName)

	err := p.interceptor.UpdateRules(p.cidrRegistry.AllCIDRs())
	if err != nil {
		return fmt.Errorf("failed to update interception rules after deregistering cluster %s: %w",
			clusterName, err)
	}

	return nil
}

func (p *Proxy) acceptLoop(ctx context.Context, listener net.Listener) {
	logger := logging.FromContext(ctx)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				logger.Error("Failed to accept connection", zap.Error(err))

				continue
			}
		}

		go p.handleConnection(ctx, conn)
	}
}

func (p *Proxy) cleanup() error {
	var cleanupErr error

	p.stopOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.listener != nil {
			err := p.listener.Close()
			if err != nil {
				cleanupErr = fmt.Errorf("failed to close listener: %w", err)
			}
		}

		p.tunnelPool.CloseAll()

		err := p.interceptor.Cleanup()
		if err != nil {
			if cleanupErr != nil {
				cleanupErr = fmt.Errorf("%w; failed to cleanup interceptor: %w", cleanupErr, err)
			} else {
				cleanupErr = fmt.Errorf("failed to cleanup interceptor: %w", err)
			}
		}
	})

	return cleanupErr
}
