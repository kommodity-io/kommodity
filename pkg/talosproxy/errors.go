// Package talosproxy provides an HTTP CONNECT proxy that intercepts Talos gRPC
// connections and tunnels them through a Kubernetes port-forward to talos-cluster-proxy pods
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
	// ErrProxyPodNotFound is returned when no talos-cluster-proxy pod is found in the workload cluster.
	ErrProxyPodNotFound = errors.New("talos-cluster-proxy pod not found")
	// ErrInvalidProxyLabel is returned when the proxy label format is invalid.
	ErrInvalidProxyLabel = errors.New("invalid proxy label format")
	// ErrNoForwardedPorts is returned when no forwarded ports are returned after port-forward setup.
	ErrNoForwardedPorts = errors.New("no forwarded ports returned")
	// ErrListenerNotBound is returned when the proxy listener has not been bound yet.
	ErrListenerNotBound = errors.New("listener not bound")
	// ErrMethodNotAllowed is returned when a non-CONNECT HTTP method is received.
	ErrMethodNotAllowed = errors.New("only CONNECT method is allowed")
	// ErrInvalidConnectTarget is returned when the CONNECT target host:port is invalid.
	ErrInvalidConnectTarget = errors.New("invalid CONNECT target")
	// ErrHijackNotSupported is returned when the HTTP response writer does not support hijacking.
	ErrHijackNotSupported = errors.New("hijack not supported")
)
