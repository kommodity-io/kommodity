package kms

import (
	"context"
	"net"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const (
	headerXForwardedFor      = "x-forwarded-for"
	headerXRealIP            = "x-real-ip"
	headerXEnvoyExternalAddr = "x-envoy-external-address"
)

// extractClientIP resolves the original client IP from the gRPC context.
// It inspects proxy-injected headers in priority order before falling back
// to the direct TCP peer address:
//  1. X-Forwarded-For (leftmost entry)
//  2. X-Real-Ip
//  3. X-Envoy-External-Address
//  4. Peer address (direct TCP connection)
func extractClientIP(ctx context.Context) (string, error) {
	logger := logging.FromContext(ctx)

	incomingMD, hasMD := metadata.FromIncomingContext(ctx)

	if hasMD {
		if ip, ok := ipFromXForwardedFor(incomingMD); ok {
			logger.Info("resolved client IP from X-Forwarded-For", zap.String("ip", ip))

			return ip, nil
		}

		if ip, ok := ipFromHeader(incomingMD, headerXRealIP); ok {
			logger.Info("resolved client IP from X-Real-Ip", zap.String("ip", ip))

			return ip, nil
		}

		if ip, ok := ipFromHeader(incomingMD, headerXEnvoyExternalAddr); ok {
			logger.Info("resolved client IP from X-Envoy-External-Address", zap.String("ip", ip))

			return ip, nil
		}
	}

	if ip, ok := ipFromPeer(ctx); ok {
		logger.Info("resolved client IP from peer address", zap.String("ip", ip))

		return ip, nil
	}

	return "", ErrNoValidClientIP
}

// ipFromXForwardedFor extracts the leftmost valid IP from the
// X-Forwarded-For metadata entries. The header may contain a single
// comma-separated list or multiple metadata values.
func ipFromXForwardedFor(md metadata.MD) (string, bool) {
	values := md.Get(headerXForwardedFor)
	if len(values) == 0 {
		return "", false
	}

	// Join all entries and split by comma to handle both single
	// comma-separated values and multiple metadata entries.
	joined := strings.Join(values, ",")

	for raw := range strings.SplitSeq(joined, ",") {
		ip := sanitizeIP(strings.TrimSpace(raw))
		if ip != "" {
			return ip, true
		}
	}

	return "", false
}

// ipFromHeader extracts a valid IP from a single-value metadata header.
func ipFromHeader(md metadata.MD, header string) (string, bool) {
	values := md.Get(header)
	if len(values) == 0 {
		return "", false
	}

	ip := sanitizeIP(strings.TrimSpace(values[0]))
	if ip != "" {
		return ip, true
	}

	return "", false
}

// ipFromPeer extracts the IP from the direct TCP peer address.
func ipFromPeer(ctx context.Context) (string, bool) {
	client, ok := peer.FromContext(ctx)
	if !ok {
		return "", false
	}

	ip := sanitizeIP(client.Addr.String())
	if ip != "" {
		return ip, true
	}

	return "", false
}

// sanitizeIP takes a raw address string (with or without port) and returns
// a validated IP string. Returns an empty string if the input is not a valid IP.
func sanitizeIP(raw string) string {
	if raw == "" {
		return ""
	}

	// Try parsing as a plain IP first.
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}

	// Try stripping port from host:port format.
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return ""
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}

	return ""
}
