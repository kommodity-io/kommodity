//go:build !linux

package talosproxy

import (
	"net"
)

// GetOriginalDst is a stub for non-Linux platforms that always returns ErrOriginalDstNotAvailable.
func GetOriginalDst(_ *net.TCPConn) (*net.TCPAddr, error) {
	return nil, ErrOriginalDstNotAvailable
}
