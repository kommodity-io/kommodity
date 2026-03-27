package talosproxy

import "net"

// NewTrackedConn creates a trackedConn for testing.
func NewTrackedConn(conn net.Conn, tunnel *Tunnel) net.Conn {
	return &trackedConn{Conn: conn, tunnel: tunnel}
}
