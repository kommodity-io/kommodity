package talosproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	// connectDialTimeout is the timeout for direct passthrough connections.
	connectDialTimeout = 30 * time.Second
	// connectResponseEstablished is the HTTP response sent after a successful CONNECT.
	connectResponseEstablished = "HTTP/1.1 200 Connection Established\r\n\r\n"
)

// ConnectHandler implements an HTTP CONNECT proxy that routes connections
// matching registered CIDRs through SPDY tunnels to talos-cluster-proxy pods,
// and passes through all other traffic directly.
type ConnectHandler struct {
	cidrRegistry *CIDRRegistry
	tunnelPool   *TunnelPool
	maxRetries   int
	logger       *zap.Logger
}

// NewConnectHandler creates a new ConnectHandler.
func NewConnectHandler(
	cidrRegistry *CIDRRegistry,
	tunnelPool *TunnelPool,
	maxRetries int,
	logger *zap.Logger,
) *ConnectHandler {
	return &ConnectHandler{
		cidrRegistry: cidrRegistry,
		tunnelPool:   tunnelPool,
		maxRetries:   maxRetries,
		logger:       logger,
	}
}

// ServeHTTP handles HTTP CONNECT requests.
func (h *ConnectHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodConnect {
		http.Error(writer, ErrMethodNotAllowed.Error(), http.StatusMethodNotAllowed)

		return
	}

	targetIP, err := parseConnectTarget(request.Host)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)

		return
	}

	upstream, err := h.dialUpstream(request.Context(), targetIP, request.Host)
	if err != nil {
		h.logger.Error("Failed to dial upstream",
			zap.String("target", request.Host),
			zap.Error(err))

		http.Error(writer, err.Error(), http.StatusBadGateway)

		return
	}

	defer func() {
		closeErr := upstream.Close()
		if closeErr != nil {
			h.logger.Debug("Failed to close upstream connection", zap.Error(closeErr))
		}
	}()

	h.hijackAndPipe(writer, upstream, request.Host)
}

func (h *ConnectHandler) hijackAndPipe(writer http.ResponseWriter, upstream net.Conn, target string) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		http.Error(writer, ErrHijackNotSupported.Error(), http.StatusInternalServerError)

		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		h.logger.Error("Failed to hijack connection", zap.Error(err))

		return
	}

	defer func() {
		closeErr := clientConn.Close()
		if closeErr != nil {
			h.logger.Debug("Failed to close client connection", zap.Error(closeErr))
		}
	}()

	_, err = io.WriteString(clientConn, connectResponseEstablished)
	if err != nil {
		h.logger.Error("Failed to write CONNECT response", zap.Error(err))

		return
	}

	h.logger.Debug("Proxying connection",
		zap.String("target", target))

	bidirectionalCopy(clientConn, upstream)
}

// parseConnectTarget validates and extracts the target IP from a host:port string.
func parseConnectTarget(hostPort string) (net.IP, error) {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidConnectTarget, hostPort)
	}

	targetIP := net.ParseIP(host)
	if targetIP == nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidConnectTarget, hostPort)
	}

	return targetIP, nil
}

// dialUpstream establishes a connection to the target. If the target IP matches
// a registered CIDR, the connection goes through the tunnel pool. Otherwise, it
// dials directly (passthrough).
func (h *ConnectHandler) dialUpstream(
	ctx context.Context,
	targetIP net.IP,
	targetAddr string,
) (net.Conn, error) {
	entry, err := h.cidrRegistry.Lookup(targetIP)
	if err != nil {
		return h.dialDirect(ctx, targetAddr)
	}

	return h.dialTunnel(ctx, entry, targetAddr)
}

func (h *ConnectHandler) dialDirect(ctx context.Context, targetAddr string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: connectDialTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s directly: %w", targetAddr, err)
	}

	return conn, nil
}

// dialTunnel attempts to establish a tunnelled connection through the
// talos-cluster-proxy pod. It retries the full dial-and-handshake sequence
// up to maxRetries times, removing the tunnel on each failure so the
// next attempt builds a fresh one. This recovers from transient SPDY stream
// failures (EOF, connection reset, 502 from the proxy pod) that would
// otherwise deadlock control-plane rollouts.
func (h *ConnectHandler) dialTunnel(
	ctx context.Context,
	entry *CIDREntry,
	targetAddr string,
) (net.Conn, error) {
	var lastErr error

	for attempt := 1; attempt <= h.maxRetries; attempt++ {
		conn, err := h.dialTunnelOnce(ctx, entry)
		if err != nil {
			lastErr = err

			h.logger.Warn("Tunnel dial failed",
				zap.String("cluster", entry.ClusterName),
				zap.Int("attempt", attempt),
				zap.Int("maxAttempts", h.maxRetries),
				zap.Error(err))

			h.tunnelPool.RemoveTunnel(entry.ClusterName)

			continue
		}

		err = EstablishConnectTunnel(conn, targetAddr)
		if err != nil {
			closeErr := conn.Close()
			if closeErr != nil {
				h.logger.Debug("Failed to close tunnel connection after CONNECT handshake failure",
					zap.Error(closeErr))
			}

			lastErr = err

			h.logger.Warn("CONNECT handshake failed, removing tunnel",
				zap.String("cluster", entry.ClusterName),
				zap.String("target", targetAddr),
				zap.Int("attempt", attempt),
				zap.Int("maxAttempts", h.maxRetries),
				zap.Error(err))

			h.tunnelPool.RemoveTunnel(entry.ClusterName)

			continue
		}

		h.logger.Debug("Routed through tunnel",
			zap.String("cluster", entry.ClusterName),
			zap.String("target", targetAddr),
			zap.Int("attempts", attempt))

		return conn, nil
	}

	return nil, fmt.Errorf("failed to establish tunnel for cluster %s after %d attempts: %w",
		entry.ClusterName, h.maxRetries, lastErr)
}

func (h *ConnectHandler) dialTunnelOnce(
	ctx context.Context,
	entry *CIDREntry,
) (net.Conn, error) {
	tunnel, err := h.tunnelPool.GetOrCreateTunnel(ctx, entry.ClusterName, entry.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel for cluster %s: %w", entry.ClusterName, err)
	}

	conn, err := tunnel.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to dial through tunnel for cluster %s: %w", entry.ClusterName, err)
	}

	return conn, nil
}
