package libkapi //nolint:testpackage // exercises unexported internals directly (setupAPIServerConfig, newScheme, ...)

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/stretchr/testify/require"
)

const spikeReadHeaderTimeout = 10 * time.Second

// TestDirectMountServingSpike validates, end-to-end, the highest-risk design
// decision in the PRD: a generic apiserver with SecureServing left nil,
// ExternalAddress set explicitly, and its Handler mounted directly onto a
// caller-owned http.Server, with no self-signed TLS hop and no reverse proxy.
//
// It installs no API groups at all (that comes later, once storage wiring is
// built) - only the health/discovery routes PrepareRun installs by default -
// and confirms /healthz is reachable through a plain net/http listener, and
// that PrepareRun().RunWithContext(ctx) does not open a second socket.
func TestDirectMountServingSpike(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := FreeAddr(t)
	genericServer := buildBareGenericServer(t, addr)

	require.Nil(t, genericServer.SecureServingInfo, "expected SecureServingInfo to be nil")

	prepared := genericServer.PrepareRun()

	mux := http.NewServeMux()
	mux.Handle("/", prepared.Handler)

	var listenConfig net.ListenConfig

	realListener, err := listenConfig.Listen(ctx, "tcp", addr)
	require.NoError(t, err)

	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: spikeReadHeaderTimeout}

	go func() {
		_ = httpServer.Serve(realListener)
	}()

	t.Cleanup(func() {
		_ = httpServer.Close()
	})

	runErrCh := make(chan error, 1)

	go func() {
		runErrCh <- prepared.RunWithContext(ctx)
	}()

	WaitForHealthz(t, "http://"+addr+"/healthz")

	// If RunWithContext opened its own listener (it shouldn't, since
	// SecureServingInfo is nil), binding a second listener on the same addr
	// above would have failed already. Confirm RunWithContext is still
	// running (not exited early with an error) rather than blocked forever.
	select {
	case runErr := <-runErrCh:
		require.Fail(t, "RunWithContext exited early", "error: %v", runErr)
	default:
	}
}

func buildBareGenericServer(t *testing.T, addr string) *genericapiserver.GenericAPIServer {
	t.Helper()

	scheme, codecs, err := newScheme(nil)
	require.NoError(t, err)

	groupVersions := []schema.GroupVersion{metav1.SchemeGroupVersion}

	genericServerConfig, err := setupAPIServerConfig(
		addr, scheme, codecs, groupVersions, defaultAuthenticator(), defaultAuthorizer())
	require.NoError(t, err)

	genericServer, err := genericServerConfig.Complete().New("libkapi-spike", genericapiserver.NewEmptyDelegate())
	require.NoError(t, err)

	return genericServer
}

// WaitForHealthz polls url until it returns 200 OK or 10 seconds elapse.
// Exported so both this package's internal tests and libkapi_test's external
// (black-box) tests share one implementation instead of each keeping a copy.
func WaitForHealthz(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := httpGet(url)
		if err != nil {
			lastErr = err

			time.Sleep(50 * time.Millisecond)

			continue
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return
		}

		lastErr = errStatusNotOK(resp.StatusCode, body)

		time.Sleep(50 * time.Millisecond)
	}

	require.FailNow(t, "timed out waiting for endpoint to become healthy", "url: %s, lastErr: %v", url, lastErr)
}

// httpGet is a thin wrapper around http.Get used only by test polling loops
// with their own deadline handled by the caller's loop - not a real,
// user-facing outbound request needing per-call context/URL validation.
func httpGet(url string) (*http.Response, error) {
	return http.Get(url) //nolint:noctx,gosec,wrapcheck
}

// FreeAddr binds an ephemeral port, closes it, and returns its address, for
// tests that need a concrete addr before the real listener starts. Exported
// for the same reason as WaitForHealthz.
func FreeAddr(t *testing.T) string {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()

	err = listener.Close()
	require.NoError(t, err)

	return addr
}

type statusError struct {
	code int
	body []byte
}

func (e *statusError) Error() string {
	return http.StatusText(e.code) + ": " + string(e.body)
}

func errStatusNotOK(code int, body []byte) error {
	return &statusError{code: code, body: body}
}
