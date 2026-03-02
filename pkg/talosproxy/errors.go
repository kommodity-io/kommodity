// Package talosproxy provides a transparent TCP proxy that intercepts Talos gRPC
// connections and tunnels them through a Kubernetes port-forward to talos-proxy pods
// running inside workload clusters.
package talosproxy

import "errors"

var (
	// ErrCIDRNotFound is returned when no registered CIDR matches the given IP address.
	ErrCIDRNotFound = errors.New("no registered CIDR matches the given IP")
	// ErrTunnelNotReady is returned when the port-forward tunnel is not yet established.
	ErrTunnelNotReady = errors.New("tunnel is not ready")
	// ErrTunnelClosed is returned when the tunnel has been closed.
	ErrTunnelClosed = errors.New("tunnel is closed")
	// ErrKubeconfigNotFound is returned when the kubeconfig secret for a cluster cannot be found.
	ErrKubeconfigNotFound = errors.New("kubeconfig secret not found")
	// ErrProxyPodNotFound is returned when no talos-proxy pod is found in the workload cluster.
	ErrProxyPodNotFound = errors.New("talos-proxy pod not found")
	// ErrInvalidCIDR is returned when a CIDR string cannot be parsed.
	ErrInvalidCIDR = errors.New("invalid CIDR notation")
	// ErrOriginalDstNotAvailable is returned when the original destination cannot be read from the socket.
	ErrOriginalDstNotAvailable = errors.New("original destination not available")
	// ErrProxyNotEnabled is returned when the proxy is not enabled in configuration.
	ErrProxyNotEnabled = errors.New("talos proxy is not enabled")
	// ErrInvalidProxyLabel is returned when the proxy label format is invalid.
	ErrInvalidProxyLabel = errors.New("invalid proxy label format")
	// ErrNoForwardedPorts is returned when no forwarded ports are returned after port-forward setup.
	ErrNoForwardedPorts = errors.New("no forwarded ports returned")
	// ErrNotTCPConnection is returned when the intercepted connection is not a TCP connection.
	ErrNotTCPConnection = errors.New("connection is not a TCP connection")
)
