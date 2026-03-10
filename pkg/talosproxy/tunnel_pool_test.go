package talosproxy_test

import (
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestTunnelPool_CloseAllStopsIdleTimers(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
		IdleTimeout:    50 * time.Millisecond,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, nil, zap.NewNop())

	// CloseAll with no tunnels should not panic
	pool.CloseAll()
}

func TestTunnelPool_RemoveTunnelWithNoTunnel(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
		IdleTimeout:    50 * time.Millisecond,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, nil, zap.NewNop())

	// RemoveTunnel for non-existent cluster should not panic
	pool.RemoveTunnel("non-existent")
}

func TestTunnel_OnIdleCallback(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	idleChan := make(chan struct{}, 1)

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
		OnIdle:      func() { idleChan <- struct{}{} },
	})

	// Acquire two connections
	tunnel.AcquireConn()
	tunnel.AcquireConn()
	assert.Equal(t, int64(2), tunnel.ActiveConns())

	// Release one — should NOT trigger onIdle
	tunnel.ReleaseConn()
	assert.Equal(t, int64(1), tunnel.ActiveConns())

	select {
	case <-idleChan:
		t.Fatal("onIdle should not be called when activeConns > 0")
	default:
	}

	// Release the last one — should trigger onIdle
	tunnel.ReleaseConn()
	assert.Equal(t, int64(0), tunnel.ActiveConns())

	select {
	case <-idleChan:
		// expected
	case <-time.After(time.Second):
		t.Fatal("onIdle was not called after all connections were released")
	}
}

func TestTunnel_NoOnIdleCallback(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	// Creating a tunnel without OnIdle should not panic on ReleaseConn
	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	tunnel.AcquireConn()
	tunnel.ReleaseConn()

	assert.Equal(t, int64(0), tunnel.ActiveConns())
}
