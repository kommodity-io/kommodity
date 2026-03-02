//go:build linux

package talosproxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// SO_ORIGINAL_DST is the socket option to retrieve the original destination address.
	soOriginalDst = 80
)

// GetOriginalDst retrieves the original destination address from a redirected TCP connection
// using the SO_ORIGINAL_DST socket option (set by nftables REDIRECT).
func GetOriginalDst(conn *net.TCPConn) (*net.TCPAddr, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("failed to get raw connection: %w", err)
	}

	var addr *net.TCPAddr
	var sockoptErr error

	controlErr := rawConn.Control(func(fd uintptr) {
		// getsockopt(fd, SOL_IP, SO_ORIGINAL_DST, &addr, &len)
		raw, err := unix.GetsockoptIPv6Mreq(int(fd), unix.SOL_IP, soOriginalDst)
		if err != nil {
			sockoptErr = fmt.Errorf("getsockopt SO_ORIGINAL_DST failed: %w", err)
			return
		}

		// The result is a sockaddr_in structure packed into the IPv6Mreq
		// struct: family(2) + port(2) + ip(4)
		mreqBytes := (*[unsafe.Sizeof(*raw)]byte)(unsafe.Pointer(raw))

		port := binary.BigEndian.Uint16(mreqBytes[2:4])
		ip := net.IPv4(mreqBytes[4], mreqBytes[5], mreqBytes[6], mreqBytes[7])

		addr = &net.TCPAddr{
			IP:   ip,
			Port: int(port),
		}
	})
	if controlErr != nil {
		return nil, fmt.Errorf("raw connection control failed: %w", controlErr)
	}

	if sockoptErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrOriginalDstNotAvailable, sockoptErr)
	}

	return addr, nil
}
