package talosproxy_test

import (
	"context"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTunnel(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     15050,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	// Tunnel should not be ready yet (not established), so Dial should return ErrTunnelNotReady
	_, err := tunnel.Dial(context.Background())
	assert.ErrorIs(t, err, talosproxy.ErrTunnelNotReady)
}

func TestTunnel_Close(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     15050,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	err := tunnel.Close()
	require.NoError(t, err)

	// After close, Dial should return ErrTunnelClosed
	_, err = tunnel.Dial(context.Background())
	require.ErrorIs(t, err, talosproxy.ErrTunnelClosed)

	// Double close should be safe
	err = tunnel.Close()
	require.NoError(t, err)
}

func TestTunnel_DialBeforeEstablish(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     15050,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	_, err := tunnel.Dial(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, talosproxy.ErrTunnelNotReady)
}

func TestTunnel_DialAfterClose(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     15050,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	err := tunnel.Close()
	require.NoError(t, err)

	_, err = tunnel.Dial(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, talosproxy.ErrTunnelClosed)
}
