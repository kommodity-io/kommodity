//nolint:testpackage // white-box tests exercise the unexported verifyEmbeddedWebhooksAreServed
package webhook

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// newTestManager builds a controller-runtime Manager wired to a loopback address that is
// never dialed: manager construction is lazy, and webhook registration only touches the
// scheme and the (in-memory) webhook mux, so no API server is needed to assert wiring.
func newTestManager(t *testing.T) ctrl.Manager {
	t.Helper()

	scheme := newWebhookTestScheme(t)

	mgr, err := ctrl.NewManager(unreachableRESTConfig(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	require.NoError(t, err)

	return mgr
}

// TestVerifyEmbeddedWebhooksAreServedFailsOnFreshManager proves the guard has teeth: a
// manager whose webhook server has no handlers registered must fail verification, listing
// the unserved paths. Without this check, a wiring gap like the missing
// AzureClusterIdentity handler ships silently and only surfaces as admission failures
// ("failed calling webhook ... the server could not find the requested resource").
func TestVerifyEmbeddedWebhooksAreServedFailsOnFreshManager(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mgr := newTestManager(t)

	err := verifyEmbeddedWebhooksAreServed(ctx, mgr)
	require.Error(t, err)

	require.ErrorIs(t, err, ErrUnservedWebhookPaths)

	paths, pathErr := provider.ServedWebhookPaths()
	require.NoError(t, pathErr)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		assert.Contains(t, err.Error(), path, "missing path %s must be listed in the error", path)
	}
}

// TestSetupWebhooksRegistersEveryEmbeddedPath runs the real SetupWebhooks against a
// manager and asserts the full invariant: every servable path from the embedded webhook
// configurations has a handler registered on the in-process webhook server, so admission
// never dials a path the server would 404.
func TestSetupWebhooksRegistersEveryEmbeddedPath(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mgr := newTestManager(t)

	// The cluster cache is only stored on the CAPI Cluster webhook struct at setup time
	// and is dereferenced when handling admission requests, never during registration.
	err := SetupWebhooks(ctx, &mgr, nil)
	require.NoError(t, err)

	paths, err := provider.ServedWebhookPaths()
	require.NoError(t, err)

	mux := mgr.GetWebhookServer().WebhookMux()
	require.NotNil(t, mux)

	for _, path := range paths {
		handler, pattern := mux.Handler(&http.Request{URL: &url.URL{Path: path}})
		require.NotNil(t, handler, "no handler registered for %s", path)
		assert.Equal(t, path, pattern, "handler for %s must match exactly, not a catch-all", path)
	}
}

// TestSetupWebhooksRegistersAzureClusterIdentityHandler pins the handler the bug report
// is about: without it, every AzureClusterIdentity CREATE/UPDATE was rejected because the
// API server dialed https://localhost:<port>/validate-infrastructure-cluster-x-k8s-io-
// v1beta1-azureclusteridentity and the in-process server 404ed (failurePolicy: Fail).
func TestSetupWebhooksRegistersAzureClusterIdentityHandler(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mgr := newTestManager(t)

	err := SetupWebhooks(ctx, &mgr, nil)
	require.NoError(t, err)

	mux := mgr.GetWebhookServer().WebhookMux()

	handler, pattern := mux.Handler(&http.Request{URL: &url.URL{
		Path: "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureclusteridentity",
	}})
	require.NotNil(t, handler)
	assert.Equal(t, "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureclusteridentity", pattern)
}
