package talosproxy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

var errPortForwardDiedForTest = errors.New("port-forward died")

func TestMonitorPortForward_MarksTunnelClosed(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	assert.False(t, tunnel.IsClosed(), "tunnel must start open")

	errChan := make(chan error, 1)
	errChan <- errPortForwardDiedForTest

	talosproxy.MonitorPortForwardForTest(tunnel, errChan, zap.NewNop())

	assert.True(t, tunnel.IsClosed(),
		"monitor must mark tunnel closed when port-forward returns an error")
}

func TestMonitorPortForward_NilErrAlsoMarksClosed(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
	}

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "test-cluster",
		Config:      proxyConfig,
	})

	errChan := make(chan error, 1)
	errChan <- nil

	talosproxy.MonitorPortForwardForTest(tunnel, errChan, zap.NewNop())

	assert.True(t, tunnel.IsClosed(),
		"clean port-forward exit must still mark tunnel closed for stale-tunnel detection")
}

func TestTunnelPool_IdleTimeoutLifecycle(t *testing.T) {
	t.Parallel()

	const idleTimeout = 25 * time.Millisecond

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		IdleTimeout:    idleTimeout,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, nil, zap.NewNop())

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "lifecycle-cluster",
		Config:      proxyConfig,
		OnIdle:      func() { talosproxy.PoolScheduleIdleClose(pool, "lifecycle-cluster") },
	})

	talosproxy.InjectTunnelForTest(pool, "lifecycle-cluster", tunnel)
	assert.True(t, talosproxy.PoolHasTunnel(pool, "lifecycle-cluster"),
		"injected tunnel must be present")

	tunnel.AcquireConn()
	tunnel.ReleaseConn()

	assert.Eventually(t, func() bool {
		return !talosproxy.PoolHasTunnel(pool, "lifecycle-cluster")
	}, time.Second, 5*time.Millisecond,
		"idle timer must remove tunnel from pool after timeout")

	assert.True(t, tunnel.IsClosed(),
		"removed tunnel must be closed so the next request rebuilds it")
}

func TestTunnelPool_ActiveConnectionsCancelIdleTimer(t *testing.T) {
	t.Parallel()

	const idleTimeout = 25 * time.Millisecond

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		IdleTimeout:    idleTimeout,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, nil, zap.NewNop())

	tunnel := talosproxy.NewTunnel(talosproxy.TunnelDeps{
		ClusterName: "busy-cluster",
		Config:      proxyConfig,
		OnIdle:      func() { talosproxy.PoolScheduleIdleClose(pool, "busy-cluster") },
	})

	talosproxy.InjectTunnelForTest(pool, "busy-cluster", tunnel)

	// Drive count to 0 → schedules timer, then back to 1 before timer fires.
	tunnel.AcquireConn()
	tunnel.ReleaseConn()
	tunnel.AcquireConn()

	time.Sleep(2 * idleTimeout)

	assert.True(t, talosproxy.PoolHasTunnel(pool, "busy-cluster"),
		"timer must not remove a tunnel that is back to being in-use")
	assert.False(t, tunnel.IsClosed(),
		"in-use tunnel must remain open across stale timer firings")
}
