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

type mockInterceptor struct {
	updateCalled int
	cleanCalled  int
	lastCIDRs    []*net.IPNet
}

func (m *mockInterceptor) UpdateRules(cidrs []*net.IPNet) error {
	m.updateCalled++
	m.lastCIDRs = cidrs

	return nil
}

func (m *mockInterceptor) Cleanup() error {
	m.cleanCalled++

	return nil
}

func TestProxy_RegisterAndDeregisterCluster(t *testing.T) {
	t.Parallel()

	interceptor := &mockInterceptor{}
	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     50000,
		ProxyNamespace: "kube-system",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config:      proxyConfig,
		Client:      nil,
		Interceptor: interceptor,
	})

	_, cidr, err := net.ParseCIDR("10.200.0.0/20")
	require.NoError(t, err)

	// Register
	err = proxy.RegisterCluster("cluster-a", "default", cidr)
	require.NoError(t, err)
	assert.Equal(t, 1, interceptor.updateCalled)
	assert.Len(t, interceptor.lastCIDRs, 1)

	// Deregister
	err = proxy.DeregisterCluster("cluster-a")
	require.NoError(t, err)
	assert.Equal(t, 2, interceptor.updateCalled)
	assert.Empty(t, interceptor.lastCIDRs)
}

func TestProxy_StartDisabled(t *testing.T) {
	t.Parallel()

	interceptor := &mockInterceptor{}
	proxyConfig := &config.TalosProxyConfig{
		Enabled: false,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config:      proxyConfig,
		Client:      nil,
		Interceptor: interceptor,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := proxy.Start(ctx)
	require.NoError(t, err)
}

func TestProxy_StartAndStop(t *testing.T) {
	t.Parallel()

	interceptor := &mockInterceptor{}
	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0, // Use random port
		ProxyNamespace: "kube-system",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	proxy := talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config:      proxyConfig,
		Client:      nil,
		Interceptor: interceptor,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)

	go func() {
		errChan <- proxy.Start(ctx)
	}()

	// Give the proxy time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	err := <-errChan
	require.NoError(t, err)
	assert.Equal(t, 1, interceptor.cleanCalled)
}
