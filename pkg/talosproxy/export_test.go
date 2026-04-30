package talosproxy

import (
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
