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
// matching registered CIDRs through SPDY tunnels to talos-proxy pods,
// and passes through all other traffic directly.
type ConnectHandler struct {
	cidrRegistry *CIDRRegistry
	tunnelPool   *TunnelPool
	logger       *zap.Logger
}

// NewConnectHandler creates a new ConnectHandler.
func NewConnectHandler(
	cidrRegistry *CIDRRegistry,
	tunnelPool *TunnelPool,
	logger *zap.Logger,
) *ConnectHandler {
	return &ConnectHandler{
		cidrRegistry: cidrRegistry,
		tunnelPool:   tunnelPool,
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

func (h *ConnectHandler) dialTunnel(
	ctx context.Context,
	entry *CIDREntry,
	targetAddr string,
) (net.Conn, error) {
	conn, err := h.dialTunnelOnce(ctx, entry)
	if err != nil {
		// Tunnel may be stale (e.g., talos-proxy pod was evicted during rollout).
		// Remove it and retry once with a fresh tunnel for transparent recovery.
		h.logger.Warn("Tunnel dial failed, retrying with fresh tunnel",
			zap.String("cluster", entry.ClusterName),
			zap.Error(err))

		h.tunnelPool.RemoveTunnel(entry.ClusterName)

		conn, err = h.dialTunnelOnce(ctx, entry)
		if err != nil {
			h.tunnelPool.RemoveTunnel(entry.ClusterName)

			return nil, fmt.Errorf("failed to dial through fresh tunnel for cluster %s: %w", entry.ClusterName, err)
		}
	}

	err = WriteTargetAddress(conn, targetAddr)
	if err != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			h.logger.Debug("Failed to close tunnel connection after header write failure", zap.Error(closeErr))
		}

		return nil, fmt.Errorf("failed to write target address header: %w", err)
	}

	h.logger.Debug("Routed through tunnel",
		zap.String("cluster", entry.ClusterName),
		zap.String("target", targetAddr))

	return conn, nil
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
