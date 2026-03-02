//go:build !linux

package talosproxy

import (
	"net"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
)

// StubInterceptor is a no-op interceptor for non-Linux platforms (development).
type StubInterceptor struct {
	localPort int
}

// NewStubInterceptor creates a no-op interceptor that logs warnings on non-Linux platforms.
func NewStubInterceptor(localPort int) *StubInterceptor {
	return &StubInterceptor{
		localPort: localPort,
	}
}

// UpdateRules is a no-op on non-Linux platforms.
func (s *StubInterceptor) UpdateRules(cidrs []*net.IPNet) error {
	logger := logging.NewLogger()
	logger.Warn("nftables interception is not available on this platform",
		zap.Int("localPort", s.localPort),
		zap.Int("cidrCount", len(cidrs)))

	return nil
}

// Cleanup is a no-op on non-Linux platforms.
func (s *StubInterceptor) Cleanup() error {
	return nil
}
