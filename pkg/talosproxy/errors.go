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
	// ErrProxyAlreadyConfigured is returned when HTTPS_PROXY is already set and cannot be overridden.
	ErrProxyAlreadyConfigured = errors.New("HTTPS_PROXY is already set")
	// ErrKubeconfigNotFound is returned when the kubeconfig secret for a cluster cannot be found.
	ErrKubeconfigNotFound = errors.New("kubeconfig secret not found")
	// ErrProxyPodNotFound is returned when no talos-cluster-proxy pod is found in the workload cluster.
	ErrProxyPodNotFound = errors.New("talos-cluster-proxy pod not found")
	// ErrProxyServiceNotFound is returned when the talos-cluster-proxy service cannot be found.
	ErrProxyServiceNotFound = errors.New("talos-cluster-proxy service not found")
	// ErrProxyServiceNoSelector is returned when the talos-cluster-proxy service has no pod selector.
	ErrProxyServiceNoSelector = errors.New("talos-cluster-proxy service has no selector")
	// ErrProxyServiceNoPorts is returned when the talos-cluster-proxy service has no ports defined.
	ErrProxyServiceNoPorts = errors.New("talos-cluster-proxy service has no ports")
	// ErrTargetPortNotResolvable is returned when a service named target port cannot be resolved
	// to a numeric container port on the selected pod.
	ErrTargetPortNotResolvable = errors.New("service target port not resolvable on pod")
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
	// ErrConnectRejected is returned when the talos-cluster-proxy pod rejects a CONNECT request
	// with a non-200 status code.
	ErrConnectRejected = errors.New("talos-cluster-proxy rejected CONNECT request")
	// ErrConnectMalformedResponse is returned when the CONNECT response status line cannot be parsed.
	ErrConnectMalformedResponse = errors.New("malformed CONNECT response status line")
	// ErrConnectResponseTooLarge is returned when a CONNECT response header line exceeds the size limit.
	ErrConnectResponseTooLarge = errors.New("CONNECT response header line exceeds size limit")
)
