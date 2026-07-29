package talosproxy

import (
	"context"
	"net"

	"go.uber.org/zap"
)

// NewTrackedConn creates a trackedConn for testing.
func NewTrackedConn(conn net.Conn, tunnel *Tunnel) net.Conn {
	return &trackedConn{Conn: conn, tunnel: tunnel}
}

// MonitorPortForwardForTest invokes the private monitorPortForward goroutine
// with a caller-supplied errChan so tests can drive its state transitions.
func MonitorPortForwardForTest(tunnel *Tunnel, errChan <-chan error, logger *zap.Logger) {
	tunnel.monitorPortForward(errChan, logger)
}

// InjectTunnelForTest injects a fully-formed Tunnel into the pool, bypassing
// the network-bound Establish call. Used to exercise pool lifecycle
// (idle timer, removal) without a real cluster.
func InjectTunnelForTest(pool *TunnelPool, clusterName string, tunnel *Tunnel) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	pool.tunnels[clusterName] = tunnel
}

// PoolHasTunnel reports whether the pool currently tracks a tunnel for the
// given cluster. Used to assert idle-timer cleanup.
func PoolHasTunnel(pool *TunnelPool, clusterName string) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	_, exists := pool.tunnels[clusterName]

	return exists
}

// PoolScheduleIdleClose triggers the idle-close path the same way ReleaseConn
// does, but without requiring an actual onIdle wiring.
func PoolScheduleIdleClose(pool *TunnelPool, clusterName string) {
	pool.scheduleIdleClose(clusterName)
}

// CIDRRegistryForTest exposes the proxy's CIDR registry so reconciler tests
// can assert registration/deregistration without going through dialUpstream.
func (p *Proxy) CIDRRegistryForTest() *CIDRRegistry {
	return p.cidrRegistry
}

// SetTunnelDialFunc overrides the Dial method of a Tunnel for testing.
// The provided function is called instead of dialing the local port-forward
// port, allowing tests to return controllable net.Conn instances.
func SetTunnelDialFunc(tunnel *Tunnel, fn func(ctx context.Context) (net.Conn, error)) {
	tunnel.dialFunc = fn
}

// SetPoolDialFunc sets a custom dial function on the TunnelPool. All new
// tunnels created by the pool will use this function instead of real
// port-forward connections, bypassing network establishment entirely.
func SetPoolDialFunc(pool *TunnelPool, fn func(ctx context.Context) (net.Conn, error)) {
	pool.dialFunc = fn
}

// DialTunnelForTest exposes the private dialTunnel method for testing.
func DialTunnelForTest(
	ctx context.Context,
	handler *ConnectHandler,
	entry *CIDREntry,
	targetAddr string,
) (net.Conn, error) {
	return handler.dialTunnel(ctx, entry, targetAddr)
}

// LookupEntryForTest exposes the private CIDRRegistry.Lookup for testing.
func LookupEntryForTest(registry *CIDRRegistry, ip string) (*CIDREntry, error) {
	return registry.Lookup(net.ParseIP(ip))
}
