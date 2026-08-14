package libkapi //nolint:testpackage // exercises waitForWebhookServer/resolvedWebhookAddr, which are unexported.

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	certutil "k8s.io/client-go/util/cert"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// gcWebhookTestListenDelay is how long
	// TestWaitForWebhookServer_BlocksUntilDialable waits before actually
	// starting the TLS listener it dials against.
	gcWebhookTestListenDelay = 300 * time.Millisecond

	// gcWebhookTestTimeout bounds waitForWebhookServer calls expected to
	// succeed.
	gcWebhookTestTimeout = 5 * time.Second

	// gcWebhookTestShortTimeout bounds waitForWebhookServer calls expected
	// to time out, kept short so the test itself stays fast.
	gcWebhookTestShortTimeout = 400 * time.Millisecond
)

// TestWaitForWebhookServer_BlocksUntilDialable is the regression test for
// the race described in WithGarbageCollector's doc: it proves
// waitForWebhookServer does not return before a TLS listener at addr is
// actually accepting connections - ordering, not just eventual success -
// by starting the listener only after a deliberate delay and asserting the
// call took at least that long.
func TestWaitForWebhookServer_BlocksUntilDialable(t *testing.T) {
	t.Parallel()

	addr := FreeAddr(t)

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, nil)
	require.NoError(t, err)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}

	listenerCh := make(chan net.Listener, 1)
	errCh := make(chan error, 1)

	go func() {
		time.Sleep(gcWebhookTestListenDelay)

		listener, listenErr := tls.Listen("tcp", addr, tlsConfig)
		if listenErr != nil {
			errCh <- listenErr

			return
		}

		listenerCh <- listener

		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			// waitForWebhookServer's dial (like webhook.DefaultServer's own
			// StartedChecker) performs a full client-side TLS handshake, so
			// the server side must actually complete its half - closing
			// without it would surface as a handshake EOF, not success.
			if tlsConn, ok := conn.(*tls.Conn); ok {
				_ = tlsConn.HandshakeContext(context.Background())
			}

			_ = conn.Close()
		}
	}()

	start := time.Now()
	err = waitForWebhookServer(context.Background(), addr, gcWebhookTestTimeout)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, gcWebhookTestListenDelay,
		"expected waitForWebhookServer to block until the listener actually started")

	select {
	case listener := <-listenerCh:
		t.Cleanup(func() {
			_ = listener.Close()
		})
	case listenErr := <-errCh:
		t.Fatalf("failed to start delayed test TLS listener: %v", listenErr)
	default:
		t.Fatal("expected the delayed listener to already be running once waitForWebhookServer returned")
	}
}

// TestWaitForWebhookServer_TimesOut verifies waitForWebhookServer fails
// loudly, wrapping ErrGarbageCollectorWebhookNotReady, instead of hanging
// forever or silently proceeding when nothing ever listens on addr.
func TestWaitForWebhookServer_TimesOut(t *testing.T) {
	t.Parallel()

	addr := FreeAddr(t)

	err := waitForWebhookServer(context.Background(), addr, gcWebhookTestShortTimeout)

	require.ErrorIs(t, err, ErrGarbageCollectorWebhookNotReady)
	assert.Contains(t, err.Error(), "timed out", "a genuine timeout should be reported as one, not a cancellation")
}

// TestWaitForWebhookServer_RespectsContextCancellation verifies a cancelled
// ctx stops waitForWebhookServer immediately rather than waiting out the
// full timeout, and that the resulting error is worded as a cancellation
// rather than a timeout (they're distinct failure modes: a cancelled parent
// ctx during manager shutdown isn't "not ready in time").
func TestWaitForWebhookServer_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	addr := FreeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := waitForWebhookServer(ctx, addr, gcWebhookTestTimeout)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, ErrGarbageCollectorWebhookNotReady)
	assert.Less(t, elapsed, gcWebhookTestTimeout, "expected ctx cancellation to short-circuit the wait")
	assert.Contains(t, err.Error(), "context canceled")
	assert.NotContains(t, err.Error(), "timed out",
		"a cancelled parent ctx should not be reported as a timeout")
}

// TestResolvedWebhookAddr verifies resolvedWebhookAddr always binds to
// webhookHost, and falls back to webhook.DefaultPort when cfg.Port is left
// at zero.
func TestResolvedWebhookAddr(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "127.0.0.1:9443", resolvedWebhookAddr(&WebhookConfig{}))
	assert.Equal(t, "127.0.0.1:12345", resolvedWebhookAddr(&WebhookConfig{Port: 12345}))
}
