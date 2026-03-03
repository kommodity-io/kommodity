package talosproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// readHeaderTimeout is the maximum duration for reading request headers.
	readHeaderTimeout = 10 * time.Second
)

// ProxyDeps holds the dependencies needed to create a Proxy.
type ProxyDeps struct {
	Config *config.TalosProxyConfig
	Client client.Client
	Logger *zap.Logger
}

// Proxy is the main proxy struct that manages the lifecycle of the Talos HTTP CONNECT proxy.
// It implements manager.Runnable so it can be started and stopped with the controller manager.
type Proxy struct {
	config       *config.TalosProxyConfig
	cidrRegistry *CIDRRegistry
	tunnelPool   *TunnelPool
	httpServer   *http.Server
	listener     net.Listener
	mu           sync.Mutex
	stopOnce     sync.Once
}

// NewProxy creates a new Proxy instance.
func NewProxy(deps ProxyDeps) *Proxy {
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Proxy{
		config:       deps.Config,
		cidrRegistry: NewCIDRRegistry(),
		tunnelPool:   NewTunnelPool(deps.Config, deps.Client, logger),
	}
}

// Listen binds the TCP listener without starting the HTTP server.
// This must be called before Start so that the listen address is available
// for configuring proxy environment variables before the manager starts.
func (p *Proxy) Listen(ctx context.Context) error {
	if !p.config.Enabled {
		return nil
	}

	listenAddr := fmt.Sprintf("%s:%d", loopbackAddress, p.config.ListenPort)

	listenConfig := net.ListenConfig{}

	listener, err := listenConfig.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	p.mu.Lock()
	p.listener = listener
	p.mu.Unlock()

	return nil
}

// Addr returns the bound listener address. Returns an empty string if the
// listener has not been bound yet.
func (p *Proxy) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener == nil {
		return ""
	}

	return p.listener.Addr().String()
}

// Start begins serving HTTP CONNECT requests on the pre-bound listener.
// This implements the manager.Runnable interface.
func (p *Proxy) Start(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	if !p.config.Enabled {
		logger.Info("Talos proxy is disabled, not starting")

		return nil
	}

	p.mu.Lock()
	listener := p.listener
	p.mu.Unlock()

	if listener == nil {
		return ErrListenerNotBound
	}

	handler := NewConnectHandler(p.cidrRegistry, p.tunnelPool, logger)

	p.mu.Lock()
	p.httpServer = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	p.mu.Unlock()

	logger.Info("Talos proxy started",
		zap.String("listenAddr", listener.Addr().String()))

	go func() {
		<-ctx.Done()

		logger.Info("Talos proxy shutting down")

		_ = p.cleanup()
	}()

	err := p.httpServer.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP CONNECT proxy server error: %w", err)
	}

	return nil
}

// RegisterCluster registers a new cluster CIDR with the proxy.
func (p *Proxy) RegisterCluster(clusterName string, namespace string, cidr *net.IPNet) {
	p.cidrRegistry.Register(clusterName, namespace, cidr)
}

// DeregisterCluster removes a cluster from the proxy.
func (p *Proxy) DeregisterCluster(clusterName string) {
	p.cidrRegistry.Deregister(clusterName)
	p.tunnelPool.RemoveTunnel(clusterName)
}

func (p *Proxy) cleanup() error {
	var cleanupErr error

	p.stopOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.httpServer != nil {
			err := p.httpServer.Close()
			if err != nil {
				cleanupErr = fmt.Errorf("failed to close HTTP server: %w", err)
			}
		}

		p.tunnelPool.CloseAll()
	})

	return cleanupErr
}
