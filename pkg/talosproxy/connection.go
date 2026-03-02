package talosproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
)

const (
	// bidirectionalCopyCount is the number of goroutines used for bidirectional copy.
	bidirectionalCopyCount = 2
)

// handleConnection processes a single intercepted TCP connection by:
// 1. Reading the original destination via SO_ORIGINAL_DST
// 2. Looking up the cluster from the CIDR registry
// 3. Establishing a tunnel connection to the talos-proxy pod
// 4. Writing the target address header
// 5. Performing bidirectional byte copy.
func (p *Proxy) handleConnection(ctx context.Context, conn net.Conn) {
	logger := logging.FromContext(ctx)

	defer func() {
		err := conn.Close()
		if err != nil {
			logger.Debug("Failed to close client connection", zap.Error(err))
		}
	}()

	origAddr, entry, err := p.resolveOriginalDestination(ctx, conn)
	if err != nil {
		logger.Error("Failed to resolve original destination", zap.Error(err))

		return
	}

	tunnelConn, err := p.dialTunnel(ctx, entry)
	if err != nil {
		logger.Error("Failed to dial tunnel",
			zap.String("cluster", entry.ClusterName),
			zap.Error(err))

		return
	}

	defer func() {
		err := tunnelConn.Close()
		if err != nil {
			logger.Debug("Failed to close tunnel connection", zap.Error(err))
		}
	}()

	targetAddr := origAddr.String()

	err = WriteTargetAddress(tunnelConn, targetAddr)
	if err != nil {
		logger.Error("Failed to write target address header",
			zap.String("target", targetAddr),
			zap.Error(err))

		return
	}

	logger.Debug("Proxying connection",
		zap.String("cluster", entry.ClusterName),
		zap.String("target", targetAddr))

	bidirectionalCopy(conn, tunnelConn)
}

// resolveOriginalDestination extracts the original destination from the intercepted
// connection and looks up the cluster from the CIDR registry.
func (p *Proxy) resolveOriginalDestination(
	ctx context.Context,
	conn net.Conn,
) (*net.TCPAddr, *CIDREntry, error) {
	logger := logging.FromContext(ctx)

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, nil, ErrNotTCPConnection
	}

	origAddr, err := GetOriginalDst(tcpConn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get original destination: %w", err)
	}

	logger.Debug("Intercepted connection",
		zap.String("originalDst", origAddr.String()),
		zap.String("remoteAddr", conn.RemoteAddr().String()))

	entry, err := p.cidrRegistry.Lookup(origAddr.IP)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup CIDR for IP %s: %w", origAddr.IP.String(), err)
	}

	return origAddr, entry, nil
}

func (p *Proxy) dialTunnel(ctx context.Context, entry *CIDREntry) (net.Conn, error) {
	tunnel, err := p.tunnelPool.GetOrCreateTunnel(ctx, entry.ClusterName, entry.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel for cluster %s: %w", entry.ClusterName, err)
	}

	conn, err := tunnel.Dial(ctx)
	if err != nil {
		// Tunnel may be stale; remove it so next attempt creates a fresh one
		p.tunnelPool.RemoveTunnel(entry.ClusterName)

		return nil, fmt.Errorf("failed to dial through tunnel for cluster %s: %w", entry.ClusterName, err)
	}

	return conn, nil
}

// bidirectionalCopy copies data between two connections in both directions.
// It waits for both directions to complete before returning.
func bidirectionalCopy(clientConn net.Conn, tunnel net.Conn) {
	var waitGroup sync.WaitGroup

	waitGroup.Add(bidirectionalCopyCount)

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(tunnel, clientConn)
		// Signal write-half close if supported
		if tc, ok := tunnel.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(clientConn, tunnel)
		// Signal write-half close if supported
		if tc, ok := clientConn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	waitGroup.Wait()
}
