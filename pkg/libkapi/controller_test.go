package libkapi_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webhookDialTimeout bounds a single dial attempt while polling for the
// webhook server to come up.
const webhookDialTimeout = 250 * time.Millisecond

// registryRunnable creates a ConfigMap once it starts running (proving the
// Manager's client is privileged, not anonymous - this can only happen
// after mgr.Start, once the API server is actually listening; see the note
// on SetupWithManager below), then deletes it when its context is canceled,
// using a fresh context - the same pattern kontinuum's own heartbeat
// runnable uses to delete its object on shutdown. created/done are closed
// at each respective point so a test can observe them.
type registryRunnable struct {
	client          client.Client
	name, namespace string
	created, done   chan struct{}
}

// Start deliberately creates deleteCtx from context.Background(), not ctx
// (which is already canceled by this point) - the fresh-context-on-cleanup
// pattern this test exists to verify.
//
//nolint:contextcheck
func (r *registryRunnable) Start(ctx context.Context) error {
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: r.name, Namespace: r.namespace}}

	err := r.client.Create(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to create test ConfigMap: %w", err)
	}

	close(r.created)

	<-ctx.Done()

	deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = r.client.Delete(deleteCtx, configMap)

	close(r.done)

	return nil
}

// registryTestController registers a registryRunnable. SetupWithManager
// itself only registers work - it must NOT use mgr.GetClient() directly,
// since it runs synchronously during New, before ListenAndServe has bound
// any listener; a Runnable's Start (which only runs after mgr.Start, i.e.
// after the listener is up) is where real API calls belong.
type registryTestController struct {
	runnable *registryRunnable
}

func (c *registryTestController) SetupWithManager(mgr libkapi.Manager) error {
	c.runnable.client = mgr.GetClient()

	err := mgr.Add(c.runnable)
	if err != nil {
		return fmt.Errorf("failed to register test runnable: %w", err)
	}

	return nil
}

// TestServerEndToEnd_WithController verifies the whole WithController
// lifecycle: a Runnable's client is privileged (it can create a real
// object once the manager starts), and its own shutdown cleanup
// (delete-on-ctx.Done, using a fresh context) actually completes before
// Shutdown returns - not after the listener has already closed underneath
// it.
func TestServerEndToEnd_WithController(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-controller.db")

	runnable := &registryRunnable{
		name: "controller-test", namespace: "default",
		created: make(chan struct{}), done: make(chan struct{}),
	}
	testController := &registryTestController{runnable: runnable}

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithLogger(slog.Default()),
		libkapi.WithController(testController),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	baseURL := "http://" + addr
	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	select {
	case <-runnable.created:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "timed out waiting for the runnable to create its object")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	err = server.Shutdown(shutdownCtx)
	require.NoError(t, err)

	select {
	case <-runnable.done:
	default:
		t.Fatal("expected the runnable to have finished deleting its object before Shutdown returned")
	}
}

// slowShutdownDelay is how long slowRunnable ignores ctx cancellation before
// actually stopping - deliberately much longer than shutdownDeadline below,
// so TestServerShutdown_DoesNotHangOnSlowController can prove Shutdown
// returns bounded by its own ctx instead of waiting for the runnable to
// finish.
const slowShutdownDelay = 3 * time.Second

// shutdownDeadline is the short deadline given to Shutdown's own ctx in
// TestServerShutdown_DoesNotHangOnSlowController.
const shutdownDeadline = 200 * time.Millisecond

// slowRunnable ignores ctx.Done() for slowShutdownDelay before stopping -
// simulates a Controller whose cleanup doesn't complete promptly.
type slowRunnable struct {
	stopped chan struct{}
}

func (r *slowRunnable) Start(ctx context.Context) error {
	<-ctx.Done()

	time.Sleep(slowShutdownDelay)

	close(r.stopped)

	return nil
}

// slowTestController registers a slowRunnable.
type slowTestController struct {
	runnable *slowRunnable
}

func (c *slowTestController) SetupWithManager(mgr libkapi.Manager) error {
	err := mgr.Add(c.runnable)
	if err != nil {
		return fmt.Errorf("failed to register slow runnable: %w", err)
	}

	return nil
}

// TestServerShutdown_DoesNotHangOnSlowController is a regression test for
// the bound on Shutdown's wait for the controller manager: even when a
// registered Runnable ignores ctx cancellation for far longer than
// Shutdown's own ctx allows, Shutdown must still return once its deadline
// passes, rather than blocking until the runnable actually finishes.
func TestServerShutdown_DoesNotHangOnSlowController(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-slow-shutdown.db")

	runnable := &slowRunnable{stopped: make(chan struct{})}

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithController(&slowTestController{runnable: runnable}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownDeadline)
	defer shutdownCancel()

	shutdownStart := time.Now()
	err = server.Shutdown(shutdownCtx)
	shutdownElapsed := time.Since(shutdownStart)

	// shutdownCtx has already expired by the time Shutdown moves on to
	// s.httpServer.Shutdown(ctx), so that call inherits the same expired
	// deadline and surfaces it as an error - expected, not a bug: the whole
	// point of this test is that Shutdown returns promptly instead of
	// blocking on the slow runnable, not that it returns cleanly.
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, shutdownElapsed, slowShutdownDelay,
		"Shutdown should return once its own ctx deadline passes, not wait for the slow runnable")

	select {
	case <-runnable.stopped:
		t.Fatal("expected the slow runnable to still be running when Shutdown returned")
	default:
	}

	select {
	case <-runnable.stopped:
	case <-time.After(slowShutdownDelay + time.Second):
		t.Fatal("slow runnable never actually stopped")
	}
}

// webhookTestController registers a plain 200-OK handler on the manager's
// webhook server.
type webhookTestController struct{}

func (webhookTestController) SetupWithManager(mgr libkapi.Manager) error {
	mgr.GetWebhookServer().Register("/validate", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	return nil
}

// TestServerEndToEnd_WithWebhookServer verifies that a Controller can
// register a webhook handler that's actually reachable over HTTPS, on its
// own port, using the certificate ListenAndServe adopts/creates in the
// (default) system namespace's shared Secret during its boot sequence.
func TestServerEndToEnd_WithWebhookServer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-webhook.db")

	_, webhookPortStr, err := net.SplitHostPort(libkapi.FreeAddr(t))
	require.NoError(t, err)

	// DNSNames is deliberately omitted - it defaults to []string{"localhost"},
	// which is exactly what this test dials below, proving the default
	// matches the webhook server's loopback-only bind address.
	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithController(webhookTestController{}),
		libkapi.WithWebhookServer(libkapi.WebhookConfig{
			Port: mustAtoi(t, webhookPortStr),
		}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	webhookURL := "https://" + net.JoinHostPort("localhost", webhookPortStr) + "/validate"
	waitForWebhook(t, webhookURL)

	// The certificate should also have landed in the default system
	// namespace's shared Secret - not just on disk - and WebhookCABundle
	// should expose exactly what's there, proving the sync step actually
	// ran (rather than the webhook server somehow using a stale/local-only
	// certificate).
	kubeClient, err := kubernetes.NewForConfig(&restclient.Config{Host: "http://" + addr})
	require.NoError(t, err)

	secret, err := kubeClient.CoreV1().Secrets("libkapi").Get(
		ctx, libkapi.DefaultWebhookCertSecretName, metav1.GetOptions{})
	require.NoError(t, err, "expected the webhook cert Secret to exist in the default system namespace")
	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
	assert.Equal(t, secret.Data[corev1.TLSCertKey], server.WebhookCABundle())
}

// waitForWebhook polls url (over HTTPS, trusting any cert - the server's is
// self-signed) until it returns 200 OK or 10 seconds elapse.
func waitForWebhook(t *testing.T, url string) {
	t.Helper()

	//nolint:gosec // test-only, self-signed cert
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   webhookDialTimeout,
	}

	deadline := time.Now().Add(10 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url) //nolint:noctx // test helper, url is our own freshly-built server address
		if err != nil {
			lastErr = err

			time.Sleep(50 * time.Millisecond)

			continue
		}

		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return
		}

		lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode) //nolint:err113 // test helper

		time.Sleep(50 * time.Millisecond)
	}

	require.FailNow(t, "timed out waiting for webhook to become reachable", "url: %s, lastErr: %v", url, lastErr)
}

// mustAtoi parses s as an int, failing the test immediately on error.
func mustAtoi(t *testing.T, s string) int {
	t.Helper()

	var n int

	_, err := fmt.Sscanf(s, "%d", &n)
	require.NoError(t, err)

	return n
}

// leaderElectionContenderID is the shared Lease name both contenders in
// TestLeaderElection_OnlyOneManagerBecomesLeader race for.
const leaderElectionContenderID = "libkapi-test-leader-election"

// newLeaderElectionContender builds a Manager configured to contend for
// leaderElectionContenderID's Lease against restConfig.
func newLeaderElectionContender(t *testing.T, restConfig *restclient.Config) ctrl.Manager {
	t.Helper()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
		LeaderElection:             true,
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		LeaderElectionID:           leaderElectionContenderID,
		LeaderElectionNamespace:    "default",
		LeaderElectionConfig:       restConfig,
	})
	require.NoError(t, err)

	return mgr
}

// runContender starts mgr in the background, logging (not failing) a
// non-cancellation exit - a losing contender is expected to keep blocking
// in Start until mgrCtx is canceled, not to exit on its own.
func runContender(mgrCtx context.Context, t *testing.T, mgr ctrl.Manager) {
	t.Helper()

	go func() {
		err := mgr.Start(mgrCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Log("contender exited:", err)
		}
	}()
}

// TestLeaderElection_OnlyOneManagerBecomesLeader proves the
// coordination.k8s.io Lease-based lock actually works against libkapi's own
// API: two independent Managers configured with the same leader-election ID
// against the same running Server - only one becomes leader at a time.
func TestLeaderElection_OnlyOneManagerBecomesLeader(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-leaderelection.db")

	server, err := libkapi.New(ctx, libkapi.WithAddr(addr), libkapi.WithStorage("sqlite://"+dbPath))
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr
	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	restConfig := &restclient.Config{Host: baseURL}

	mgr1 := newLeaderElectionContender(t, restConfig)
	mgr2 := newLeaderElectionContender(t, restConfig)

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	t.Cleanup(mgrCancel)

	runContender(mgrCtx, t, mgr1)
	runContender(mgrCtx, t, mgr2)

	var elected, other ctrl.Manager

	select {
	case <-mgr1.Elected():
		elected, other = mgr1, mgr2
	case <-mgr2.Elected():
		elected, other = mgr2, mgr1
	case <-time.After(10 * time.Second):
		require.FailNow(t, "timed out waiting for a leader to be elected")
	}

	assert.NotNil(t, elected)

	select {
	case <-other.Elected():
		require.FailNow(t, "expected only one manager to become leader")
	default:
	}
}

// noopTestController registers nothing - used only to make buildManager
// actually build a Manager (it's a no-op without at least one Controller),
// for tests that only care about manager-level behavior like leader
// election, not any particular reconciler/runnable.
type noopTestController struct{}

func (noopTestController) SetupWithManager(_ libkapi.Manager) error {
	return nil
}

// TestServerEndToEnd_WithLeaderElection_UsesSystemNamespace verifies that
// WithLeaderElection, given no explicit LeaderElectionConfig.Namespace,
// resolves to the configured system namespace (see WithSystemNamespace) -
// and that ListenAndServe's ensureLeaderElectionNamespace step actually
// creates that namespace before the manager tries to acquire the Lease
// there, since unlike the literal "default" namespace this used to fall
// back to, the system namespace carries no guarantee from libkapi's own
// bootstrap-default-namespace post-start hook that it already exists.
func TestServerEndToEnd_WithLeaderElection_UsesSystemNamespace(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-leaderelection-systemns.db")

	const (
		systemNamespace  = "custom-system-ns"
		leaderElectionID = "leader-election-system-ns-test"
	)

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithSystemNamespace(systemNamespace),
		libkapi.WithController(noopTestController{}),
		libkapi.WithLeaderElection(libkapi.LeaderElectionConfig{ID: leaderElectionID}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr
	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	kubeClient, err := kubernetes.NewForConfig(&restclient.Config{Host: baseURL})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, getErr := kubeClient.CoordinationV1().Leases(systemNamespace).Get(ctx, leaderElectionID, metav1.GetOptions{})

		return getErr == nil
	}, 10*time.Second, 100*time.Millisecond,
		"expected leader election Lease %q to be created in the configured system namespace %q",
		leaderElectionID, systemNamespace)
}
