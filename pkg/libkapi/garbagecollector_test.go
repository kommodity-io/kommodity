package libkapi_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/require"
)

const (
	// gcTestSyncPeriod is the resync period used by
	// TestServerEndToEnd_WithGarbageCollector - short enough to keep the test
	// fast without relying on the collector's event-driven deletion path alone.
	gcTestSyncPeriod = 200 * time.Millisecond

	// gcTestNamespace is the namespace bootstrapped by every libkapi Server (see
	// LeaderElectionConfig's doc), used here so both the owner and dependent
	// ConfigMaps land somewhere guaranteed to exist.
	gcTestNamespace = "default"

	// gcTestRequestTimeout bounds a single API call made by the test itself.
	gcTestRequestTimeout = 5 * time.Second

	// gcTestShutdownTimeout bounds the server shutdown run from t.Cleanup.
	gcTestShutdownTimeout = 10 * time.Second

	// gcTestCollectionTimeout and gcTestPollInterval bound the wait for the
	// garbage collector to actually delete the dependent object.
	gcTestCollectionTimeout = 15 * time.Second
	gcTestPollInterval      = 200 * time.Millisecond
)

// TestServerEndToEnd_WithGarbageCollector verifies the whole
// WithGarbageCollector lifecycle end-to-end: a ConfigMap with an
// ownerReference to another ConfigMap is actually deleted by the embedded
// upstream garbage collector once its owner is deleted.
func TestServerEndToEnd_WithGarbageCollector(t *testing.T) {
	t.Parallel()

	kubeClient := startGCTestServer(t)

	assertOwnerDeletionCascades(t, kubeClient, "gc-owner", "gc-dependent")
}

// TestServerEndToEnd_WithGarbageCollector_AndWebhookServer verifies that
// WithGarbageCollector and WithWebhookServer can be combined without the
// garbage collector's startup racing the webhook server's TLS listener (see
// waitForWebhookServer's doc in garbagecollector.go, and
// TestWaitForWebhookServer_BlocksUntilDialable for the isolated ordering
// proof): the server must start and become healthy, the registered webhook
// handler must be reachable, and the collector must still delete a
// dependent whose owner is removed - end-to-end evidence the readiness wait
// doesn't deadlock or break either feature.
func TestServerEndToEnd_WithGarbageCollector_AndWebhookServer(t *testing.T) {
	t.Parallel()

	kubeClient := startGCWebhookTestServer(t)

	assertOwnerDeletionCascades(t, kubeClient, "gc-webhook-owner", "gc-webhook-dependent")
}

// assertOwnerDeletionCascades creates ownerName and dependentName as
// ConfigMaps with a controller ownerReference from dependent to owner,
// deletes owner, and asserts the embedded garbage collector eventually
// deletes dependent too.
func assertOwnerDeletionCascades(t *testing.T, kubeClient kubernetes.Interface, ownerName, dependentName string) {
	t.Helper()

	owner := createGCConfigMap(t, kubeClient, ownerName, nil)

	isController := true
	blockOwnerDeletion := true

	dependent := createGCConfigMap(t, kubeClient, dependentName, []metav1.OwnerReference{{
		APIVersion:         "v1",
		Kind:               "ConfigMap",
		Name:               owner.Name,
		UID:                owner.UID,
		Controller:         &isController,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}})

	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), gcTestRequestTimeout)
	defer deleteCancel()

	err := kubeClient.CoreV1().ConfigMaps(gcTestNamespace).Delete(deleteCtx, owner.Name, metav1.DeleteOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		getCtx, getCancel := context.WithTimeout(context.Background(), gcTestRequestTimeout)
		defer getCancel()

		_, getErr := kubeClient.CoreV1().ConfigMaps(gcTestNamespace).Get(getCtx, dependent.Name, metav1.GetOptions{})

		return apierrors.IsNotFound(getErr)
	}, gcTestCollectionTimeout, gcTestPollInterval,
		"expected the garbage collector to delete the dependent ConfigMap once its owner was deleted")
}

// startGCWebhookTestServer builds and starts a libkapi Server with both
// WithGarbageCollector and WithWebhookServer enabled, waits for the server
// to become healthy and the webhook handler to become reachable, and
// returns a typed client talking to it. The server is shut down via
// t.Cleanup.
func startGCWebhookTestServer(t *testing.T) kubernetes.Interface {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-gc-webhook.db")

	_, webhookPortStr, err := net.SplitHostPort(libkapi.FreeAddr(t))
	require.NoError(t, err)

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithController(webhookTestController{}),
		libkapi.WithWebhookServer(libkapi.WebhookConfig{
			Port: mustAtoi(t, webhookPortStr),
		}),
		libkapi.WithGarbageCollector(libkapi.GarbageCollectorConfig{
			SyncPeriod: gcTestSyncPeriod,
		}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), gcTestShutdownTimeout)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr
	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	webhookURL := "https://" + net.JoinHostPort("localhost", webhookPortStr) + "/validate"
	waitForWebhook(t, webhookURL)

	kubeClient, err := kubernetes.NewForConfig(&restclient.Config{Host: baseURL})
	require.NoError(t, err)

	return kubeClient
}

// startGCTestServer builds and starts a libkapi Server with
// WithGarbageCollector enabled, waits for it to become healthy, and returns
// a typed client talking to it. The server is shut down via t.Cleanup.
func startGCTestServer(t *testing.T) kubernetes.Interface {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-gc.db")

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithGarbageCollector(libkapi.GarbageCollectorConfig{
			SyncPeriod: gcTestSyncPeriod,
		}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), gcTestShutdownTimeout)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr
	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	kubeClient, err := kubernetes.NewForConfig(&restclient.Config{Host: baseURL})
	require.NoError(t, err)

	return kubeClient
}

// createGCConfigMap creates a ConfigMap named name in gcTestNamespace, with
// the given ownerReferences, failing the test immediately on error.
func createGCConfigMap(
	t *testing.T,
	kubeClient kubernetes.Interface,
	name string,
	ownerRefs []metav1.OwnerReference,
) *corev1.ConfigMap {
	t.Helper()

	createCtx, cancel := context.WithTimeout(context.Background(), gcTestRequestTimeout)
	defer cancel()

	configMap, err := kubeClient.CoreV1().ConfigMaps(gcTestNamespace).Create(createCtx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			OwnerReferences: ownerRefs,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	return configMap
}
