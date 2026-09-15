package talosproxy

import (
	"fmt"
	"net"
	"sync"
)

// trackedConn wraps a net.Conn and decrements the tunnel's active connection
// count exactly once when closed. This ensures the tunnel's reference count
// stays accurate even if Close is called multiple times.
type trackedConn struct {
	net.Conn

	releaseOnce sync.Once
	tunnel      *Tunnel
}

// Close releases the tunnel reference count (once) and closes the underlying connection.
func (c *trackedConn) Close() error {
	c.releaseOnce.Do(func() {
		c.tunnel.ReleaseConn()
	})

	err := c.Conn.Close()
	if err != nil {
		return fmt.Errorf("failed to close tracked connection: %w", err)
	}

	return nil
}

// CloseWrite signals half-close on the underlying connection if it supports it.
func (c *trackedConn) CloseWrite() error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		err := tc.CloseWrite()
		if err != nil {
			return fmt.Errorf("failed to half-close tracked connection: %w", err)
		}
	}

	return nil
}
