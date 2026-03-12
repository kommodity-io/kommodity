package talosproxy_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxy_RegisterAndDeregisterCluster(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: proxyConfig,
		Client: nil,
	})

	_, cidr, err := net.ParseCIDR("10.200.0.0/20")
	require.NoError(t, err)

	// Register should not panic
	proxy.RegisterCluster("cluster-a", "default", cidr)

	// Deregister should not panic
	proxy.DeregisterCluster("cluster-a")
}

func TestProxy_StartDisabled(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled: false,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: proxyConfig,
		Client: nil,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := proxy.Start(ctx)
	require.NoError(t, err)
}

func TestProxy_ListenAndStart(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0, // Use random port
		ProxyNamespace: "talos-cluster-proxy",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: proxyConfig,
		Client: nil,
	})

	ctx, cancel := context.WithCancel(context.Background())

	err := proxy.Listen(ctx)
	require.NoError(t, err)

	addr := proxy.Addr()
	assert.NotEmpty(t, addr, "listener address should be non-empty after Listen")

	errChan := make(chan error, 1)

	go func() {
		errChan <- proxy.Start(ctx)
	}()

	// Give the proxy time to start serving
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	err = <-errChan
	require.NoError(t, err)
}

func TestProxy_StartWithoutListen(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: proxyConfig,
		Client: nil,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := proxy.Start(ctx)
	require.ErrorIs(t, err, talosproxy.ErrListenerNotBound)
}

func TestProxy_AddrBeforeListen(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:    true,
		ListenPort: 0,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: proxyConfig,
		Client: nil,
	})

	assert.Empty(t, proxy.Addr(), "Addr should return empty string before Listen")
}
