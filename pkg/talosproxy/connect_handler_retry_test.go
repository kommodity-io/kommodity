package talosproxy_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testMaxRetries = 3

// mockProxyConn simulates the talos-cluster-proxy pod side of a CONNECT
// handshake. When failCount > 0, the first failCount connections are closed
// immediately (producing EOF on the client side). Subsequent connections
// receive the CONNECT request and respond with 200.
type mockProxyConn struct {
	failCount int32
	calls     atomic.Int32
}

func (m *mockProxyConn) handleConn(conn net.Conn) {
	call := m.calls.Add(1)

	if call <= m.failCount {
		_ = conn.Close()

		return
	}

	buf := make([]byte, 256)

	_, err := conn.Read(buf)
	if err != nil {
		_ = conn.Close()

		return
	}

	_, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	if err != nil {
		_ = conn.Close()

		return
	}

	_ = conn.Close()
}

// poolDialFunc returns a dial function for the TunnelPool that creates
// net.Pipe connections to mockProxyConn.
func poolDialFunc(mock *mockProxyConn) func(context.Context) (net.Conn, error) {
	return func(_ context.Context) (net.Conn, error) {
		serverConn, clientConn := net.Pipe()
		go mock.handleConn(serverConn)

		return clientConn, nil
	}
}

type mockSPDYError struct{ msg string }

func (e *mockSPDYError) Error() string { return e.msg }

// failingDialFunc returns a dial function that always returns an error.
func failingDialFunc() func(context.Context) (net.Conn, error) {
	err := &mockSPDYError{msg: "simulated SPDY stream failure"}

	return func(_ context.Context) (net.Conn, error) {
		return nil, err
	}
}

// flakyDialFunc returns a dial function that fails the first failAfter
// calls, then delegates to mock for subsequent calls.
func flakyDialFunc(
	failAfter int32,
	mock *mockProxyConn,
) func(context.Context) (net.Conn, error) {
	var calls atomic.Int32

	err := &mockSPDYError{msg: "simulated SPDY stream failure"}

	return func(_ context.Context) (net.Conn, error) {
		call := calls.Add(1)
		if call <= failAfter {
			return nil, err
		}

		serverConn, clientConn := net.Pipe()
		go mock.handleConn(serverConn)

		return clientConn, nil
	}
}

func newRetryTestPool(
	t *testing.T,
	dialFunc func(context.Context) (net.Conn, error),
) *talosproxy.TunnelPool {
	t.Helper()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		MaxRetries:     testMaxRetries,
	}

	logger := zap.NewNop()
	pool := talosproxy.NewTunnelPool(proxyConfig, nil, logger)
	talosproxy.SetPoolDialFunc(pool, dialFunc)

	return pool
}

func newRetryTestRegistry(t *testing.T) *talosproxy.CIDRRegistry {
	t.Helper()

	registry := talosproxy.NewCIDRRegistry()
	_, cidr, err := net.ParseCIDR("10.200.0.0/20")
	require.NoError(t, err)
	registry.Register("test-cluster", "default", cidr)

	return registry
}

func lookupTestEntry(t *testing.T, registry *talosproxy.CIDRRegistry) *talosproxy.CIDREntry {
	t.Helper()

	entry, err := talosproxy.LookupEntryForTest(registry, "10.200.0.5")
	require.NoError(t, err)

	return entry
}

func TestDialTunnel_RetriesOnHandshakeFailure(t *testing.T) {
	t.Parallel()

	mock := &mockProxyConn{failCount: 2}
	pool := newRetryTestPool(t, poolDialFunc(mock))
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	conn, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	assert.Equal(t, int32(3), mock.calls.Load(),
		"should take 3 attempts: 2 handshake failures + 1 success")
}

func TestDialTunnel_RetriesOnDialFailure(t *testing.T) {
	t.Parallel()

	mock := &mockProxyConn{failCount: 0}
	pool := newRetryTestPool(t, flakyDialFunc(2, mock))
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	conn, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	assert.Equal(t, int32(1), mock.calls.Load(),
		"mock should only see 1 call (the successful dial)")
}

func TestDialTunnel_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	pool := newRetryTestPool(t, failingDialFunc())
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	conn, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.Error(t, err)
	require.Nil(t, conn)

	assert.Contains(t, err.Error(), "after 3 attempts")
}

func TestDialTunnel_SucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()

	mock := &mockProxyConn{failCount: 0}
	pool := newRetryTestPool(t, poolDialFunc(mock))
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	conn, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	assert.Equal(t, int32(1), mock.calls.Load(),
		"should succeed on first attempt")
}

func TestDialTunnel_RemovesTunnelAfterExhaustingRetries(t *testing.T) {
	t.Parallel()

	pool := newRetryTestPool(t, failingDialFunc())
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	_, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.Error(t, err)

	assert.False(t, talosproxy.PoolHasTunnel(pool, "test-cluster"),
		"tunnel should be removed from pool after all retries exhausted")
}

func TestDialTunnel_HandshakeAlwaysFails(t *testing.T) {
	t.Parallel()

	mock := &mockProxyConn{failCount: 99}
	pool := newRetryTestPool(t, poolDialFunc(mock))
	registry := newRetryTestRegistry(t)
	handler := talosproxy.NewConnectHandler(registry, pool, testMaxRetries, zap.NewNop())

	entry := lookupTestEntry(t, registry)

	conn, err := talosproxy.DialTunnelForTest(context.Background(), handler, entry, "10.200.0.5:50000")
	require.Error(t, err)
	require.Nil(t, conn)

	assert.Equal(t, int32(testMaxRetries), mock.calls.Load(),
		"should attempt exactly maxRetries times")

	assert.Contains(t, err.Error(), "after 3 attempts")
}
