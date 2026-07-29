package libkapi_test

import (
	"context"
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

	owner := createGCConfigMap(t, kubeClient, "gc-owner", nil)

	isController := true
	blockOwnerDeletion := true

	dependent := createGCConfigMap(t, kubeClient, "gc-dependent", []metav1.OwnerReference{{
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
