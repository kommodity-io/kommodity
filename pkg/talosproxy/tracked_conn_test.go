package talosproxy_test

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackedConn_CloseDecrementsRefCount(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	var idleCalled atomic.Bool

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
		OnIdle:      func() { idleCalled.Store(true) },
	})

	// Simulate an acquired connection
	tunnel.AcquireConn()
	assert.Equal(t, int64(1), tunnel.ActiveConns())

	// Create a tracked conn using a pipe
	server, client := net.Pipe()

	defer func() { _ = server.Close() }()

	tracked := talosproxy.NewTrackedConn(client, tunnel)

	// Close should decrement refcount
	err := tracked.Close()
	require.NoError(t, err)

	assert.Equal(t, int64(0), tunnel.ActiveConns())
	assert.True(t, idleCalled.Load(), "onIdle callback should have been called")
}

func TestTrackedConn_DoubleCloseDoesNotDoublDecrement(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	var idleCount atomic.Int32

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
		OnIdle:      func() { idleCount.Add(1) },
	})

	tunnel.AcquireConn()

	server, client := net.Pipe()

	defer func() { _ = server.Close() }()

	tracked := talosproxy.NewTrackedConn(client, tunnel)

	// First close
	err := tracked.Close()
	require.NoError(t, err)

	assert.Equal(t, int64(0), tunnel.ActiveConns())

	// Second close should not decrement again (would go negative)
	_ = tracked.Close()

	assert.Equal(t, int64(0), tunnel.ActiveConns())
	assert.Equal(t, int32(1), idleCount.Load(), "onIdle should only be called once")
}

func TestTrackedConn_ReadWritePassThrough(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-proxy",
		ProxyPort:      50000,
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	tunnel.AcquireConn()

	server, client := net.Pipe()
	tracked := talosproxy.NewTrackedConn(client, tunnel)

	testData := []byte("hello world")

	go func() {
		_, _ = server.Write(testData)
	}()

	buf := make([]byte, len(testData))

	bytesRead, err := tracked.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, len(testData), bytesRead)
	assert.Equal(t, testData, buf[:bytesRead])

	go func() {
		readBuf := make([]byte, len(testData))
		_, _ = server.Read(readBuf)
	}()

	bytesWritten, err := tracked.Write(testData)
	require.NoError(t, err)
	assert.Equal(t, len(testData), bytesWritten)

	_ = tracked.Close()
	_ = server.Close()
}
