package talosproxy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTunnelPool_CloseAllStopsIdleTimers(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
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
		ProxyLabel:     "app=talos-cluster-proxy",
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
		ProxyLabel:     "app=talos-cluster-proxy",
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

func TestTunnelPool_ConcurrentGetOrCreateTunnel_DoesNotBlock(t *testing.T) {
	t.Parallel()

	// Verify that concurrent GetOrCreateTunnel calls for different clusters
	// do not block each other even when establishment fails (nil client).
	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "kube-system",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
		IdleTimeout:    50 * time.Millisecond,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, fake.NewClientBuilder().Build(), zap.NewNop())

	const numClusters = 5

	var waitGroup sync.WaitGroup

	errChan := make(chan error, numClusters)

	for clusterIndex := range numClusters {
		waitGroup.Add(1)

		go func(idx int) {
			defer waitGroup.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			clusterName := "cluster-" + string(rune('a'+idx))

			_, err := pool.GetOrCreateTunnel(ctx, clusterName, "default")
			// Error is expected (nil client), but it must not block other clusters.
			errChan <- err
		}(clusterIndex)
	}

	waitGroup.Wait()
	close(errChan)

	var errCount int

	for err := range errChan {
		if err != nil {
			errCount++
		}
	}

	// All calls should have returned (with errors due to nil client), not timed out.
	assert.Equal(t, numClusters, errCount, "all goroutines should have returned errors, not blocked")
}

func TestTunnelPool_ConcurrentGetOrCreateTunnel_SameClusterWaits(t *testing.T) {
	t.Parallel()

	// Verify that concurrent callers for the same cluster all return after the
	// single in-progress establishment completes (with failure in this case).
	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "kube-system",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      50000,
		IdleTimeout:    50 * time.Millisecond,
	}

	pool := talosproxy.NewTunnelPool(proxyConfig, fake.NewClientBuilder().Build(), zap.NewNop())

	const numCallers = 10

	var waitGroup sync.WaitGroup

	errChan := make(chan error, numCallers)

	for range numCallers {
		waitGroup.Add(1)
		waitGroup.Go(func() {
			defer waitGroup.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := pool.GetOrCreateTunnel(ctx, "shared-cluster", "default")
			errChan <- err
		})
	}

	waitGroup.Wait()
	close(errChan)

	var returned int
	for range errChan {
		returned++
	}

	assert.Equal(t, numCallers, returned, "all concurrent callers should return, not block indefinitely")
}

func TestTunnel_NoOnIdleCallback(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "default",
		ProxyLabel:     "app=talos-cluster-proxy",
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
