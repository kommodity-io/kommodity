package kms_test

import (
	"context"
	"net"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/kms"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const testClientIP = "10.0.0.1"

func TestExtractClientIPFromXForwardedFor(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-forwarded-for", testClientIP)
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != testClientIP {
		t.Fatalf("expected %s, got %s", testClientIP, clientIP)
	}
}

func TestExtractClientIPFromXForwardedForChain(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-forwarded-for", "10.0.0.1, 10.0.0.2, 10.0.0.3")
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != testClientIP {
		t.Fatalf("expected leftmost IP %s, got %s", testClientIP, clientIP)
	}
}

func TestExtractClientIPFromXForwardedForWithSpaces(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-forwarded-for", " 10.0.0.1 , 10.0.0.2 ")
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != testClientIP {
		t.Fatalf("expected %s, got %s", testClientIP, clientIP)
	}
}

func TestExtractClientIPFromXRealIP(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-real-ip", "10.0.0.5")
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != "10.0.0.5" {
		t.Fatalf("expected 10.0.0.5, got %s", clientIP)
	}
}

func TestExtractClientIPFromEnvoyExternalAddress(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-envoy-external-address", "10.0.0.9")
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != "10.0.0.9" {
		t.Fatalf("expected 10.0.0.9, got %s", clientIP)
	}
}

func TestExtractClientIPFromPeerAddress(t *testing.T) {
	t.Parallel()

	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.42"), Port: 12345},
	})

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != "10.0.0.42" {
		t.Fatalf("expected 10.0.0.42, got %s", clientIP)
	}
}

func TestExtractClientIPXForwardedForTakesPrecedence(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs(
		"x-forwarded-for", testClientIP,
		"x-real-ip", "10.0.0.2",
		"x-envoy-external-address", "10.0.0.3",
	)
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)
	ctx = peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.4"), Port: 12345},
	})

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != testClientIP {
		t.Fatalf("expected X-Forwarded-For IP %s, got %s", testClientIP, clientIP)
	}
}

func TestExtractClientIPIPv6(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs("x-forwarded-for", "2001:db8::1")
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != "2001:db8::1" {
		t.Fatalf("expected 2001:db8::1, got %s", clientIP)
	}
}

func TestExtractClientIPInvalidHeaderFallsThrough(t *testing.T) {
	t.Parallel()

	incomingMD := metadata.Pairs(
		"x-forwarded-for", "not-an-ip",
		"x-real-ip", "10.0.0.5",
	)
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	clientIP, err := kms.ExtractClientIP(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clientIP != "10.0.0.5" {
		t.Fatalf("expected fallback to X-Real-Ip 10.0.0.5, got %s", clientIP)
	}
}

func TestExtractClientIPNoSourceReturnsError(t *testing.T) {
	t.Parallel()

	_, err := kms.ExtractClientIP(context.Background())
	if err == nil {
		t.Fatal("expected error when no IP source is available")
	}
}

func TestSanitizeIPPlain(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("192.168.1.1")
	if result != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1, got %s", result)
	}
}

func TestSanitizeIPWithPort(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("1.2.3.4:8080")
	if result != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %s", result)
	}
}

func TestSanitizeIPIPv6(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("::1")
	if result != "::1" {
		t.Fatalf("expected ::1, got %s", result)
	}
}

func TestSanitizeIPIPv6WithBracketsAndPort(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("[::1]:8080")
	if result != "::1" {
		t.Fatalf("expected ::1, got %s", result)
	}
}

func TestSanitizeIPEmpty(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("")
	if result != "" {
		t.Fatalf("expected empty string, got %s", result)
	}
}

func TestSanitizeIPInvalid(t *testing.T) {
	t.Parallel()

	result := kms.SanitizeIP("not-an-ip")
	if result != "" {
		t.Fatalf("expected empty string for invalid IP, got %s", result)
	}
}
