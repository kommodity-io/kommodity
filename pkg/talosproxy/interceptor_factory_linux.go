//go:build linux

package talosproxy

import "net"

// stubFallback is a minimal no-op interceptor used when nftables is unavailable on Linux.
type stubFallback struct{}

func (s *stubFallback) UpdateRules(_ []*net.IPNet) error { return nil }
func (s *stubFallback) Cleanup() error                   { return nil }

// NewPlatformInterceptor creates the platform-appropriate interceptor.
// On Linux, this uses nftables for transparent traffic interception.
func NewPlatformInterceptor(localPort int) Interceptor {
	interceptor, err := NewNftablesInterceptor(localPort)
	if err != nil {
		return &stubFallback{}
	}

	return interceptor
}
