//go:build !linux

package talosproxy

// NewPlatformInterceptor creates the platform-appropriate interceptor.
// On non-Linux platforms, this is a no-op stub for development.
func NewPlatformInterceptor(localPort int) Interceptor {
	return NewStubInterceptor(localPort)
}
