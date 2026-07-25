package libkapi_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errPostStartHookFailed is returned by a deliberately failing post-start
// hook in TestServerListenAndServe_PostStartHookErrorFailsStart.
var errPostStartHookFailed = errors.New("post-start hook failed on purpose")

// orderTracker records hook names in the order they were appended, safe for
// concurrent use - shared by TestServerEndToEnd_WithPostStartHook and
// TestServerEndToEnd_WithPreShutdownHook to prove hooks run in registration
// order across goroutines.
type orderTracker struct {
	mu    sync.Mutex
	order []string
}

func (o *orderTracker) append(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.order = append(o.order, name)
}

func (o *orderTracker) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]string(nil), o.order...)
}

// orderOnlyHook returns a PostStartHookFunc/PreShutdownHookFunc-compatible
// closure that only records name in tracker.
func orderOnlyHook(tracker *orderTracker, name string) func(context.Context, *restclient.Config) error {
	return func(_ context.Context, _ *restclient.Config) error {
		tracker.append(name)

		return nil
	}
}

// configMapHook returns a PostStartHookFunc/PreShutdownHookFunc-compatible
// closure that records name in tracker, then creates a ConfigMap using a
// client built from the loopbackConfig it's handed - proving that config is
// privileged and that the server is actually reachable at the point the
// hook runs.
func configMapHook(tracker *orderTracker, name, configMapName string) func(context.Context, *restclient.Config) error {
	return func(ctx context.Context, loopbackConfig *restclient.Config) error {
		tracker.append(name)

		kubeClient, err := kubernetes.NewForConfig(loopbackConfig)
		if err != nil {
			return fmt.Errorf("failed to build client from loopback config: %w", err)
		}

		configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"}}

		_, err = kubeClient.CoreV1().ConfigMaps("default").Create(ctx, configMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create test ConfigMap: %w", err)
		}

		return nil
	}
}

// TestServerEndToEnd_WithPostStartHook verifies three things at once: hooks
// run in registration order, each is handed a privileged
// (system:masters-equivalent) loopback config - proven by creating a real
// object despite WithAdminAuthorizer denying anonymous requests - and all
// post-start hooks finish before a registered Controller's Runnable starts.
func TestServerEndToEnd_WithPostStartHook(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-poststart.db")
	tracker := &orderTracker{}

	runnable := &registryRunnable{
		name: "post-start-hook-runnable", namespace: "default",
		created: make(chan struct{}), done: make(chan struct{}),
	}
	runnableController := &registryTestController{runnable: runnable}

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithAdminAuthorizer(libkapi.AdminAuthorizerConfig{AdminGroups: "test-admins"}),
		libkapi.WithPostStartHook(orderOnlyHook(tracker, "first")),
		libkapi.WithPostStartHook(configMapHook(tracker, "second", "post-start-hook-test")),
		libkapi.WithController(runnableController),
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

	// Wait for the apiserver's own internal post-start hooks (informer
	// caches etc.) to finish before this test's own fast hooks+Runnable flow
	// triggers Shutdown via t.Cleanup - Shutdown cancels the same runCtx
	// those internal hooks depend on, and canceling it while one is still
	// mid-flight makes k8s.io/apiserver treat that exactly like a timeout
	// and call klog.Fatal (crashing the whole process) - see
	// PostStartHookFunc's doc. /healthz only reports OK once every
	// registered post-start hook (ours and the apiserver's own) is done.
	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	select {
	case <-runnable.created:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "timed out waiting for the runnable to start")
	}

	// registryRunnable.Start (defined in controller_test.go) has no
	// knowledge of tracker - observing runnable.created close is itself the
	// proof it ran, so it's appended here rather than from inside Start.
	tracker.append("runnable")

	assert.Equal(t, []string{"first", "second", "runnable"}, tracker.snapshot(),
		"post-start hooks must run in registration order, before the controller manager's Runnables")
}

// crdEstablishedHook returns a PostStartHookFunc that creates a CRD named
// crdName and blocks until it becomes Established, then closes established -
// see TestServerEndToEnd_WithPostStartHook_WaitsForCRDEstablishment's doc
// for why that's the pattern under test.
func crdEstablishedHook(
	crdName, group, plural, singular, kind string, established chan<- struct{},
) func(context.Context, *restclient.Config) error {
	return func(ctx context.Context, loopbackConfig *restclient.Config) error {
		apiextensionsClient, err := apiextensionsclientset.NewForConfig(loopbackConfig)
		if err != nil {
			return fmt.Errorf("failed to build apiextensions client: %w", err)
		}

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: group,
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural: plural, Singular: singular, Kind: kind, ListKind: kind + "List",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name: "v1", Served: true, Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object", XPreserveUnknownFields: new(true),
							},
						},
					},
				},
			},
		}

		_, err = apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create CRD %q: %w", crdName, err)
		}

		err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true,
			func(ctx context.Context) (bool, error) {
				got, getErr := apiextensionsClient.ApiextensionsV1().
					CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
				if getErr != nil {
					return false, nil //nolint:nilerr // keep polling on transient errors
				}

				for _, cond := range got.Status.Conditions {
					if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
						return true, nil
					}
				}

				return false, nil
			})
		if err != nil {
			return fmt.Errorf("CRD %q never became established: %w", crdName, err)
		}

		close(established)

		return nil
	}
}

// TestServerEndToEnd_WithPostStartHook_WaitsForCRDEstablishment is a
// regression test for a real deadlock reported from a consumer: a
// WithPostStartHook that creates a CRD and waits for it to become
// Established - the pattern needed before a WithController reconciler can
// safely Watch that CRD, since controller-runtime's source.Kind.Start fails
// immediately if the CRD isn't already in the RESTMapper - used to hang
// forever. WithPostStartHook ran before NonBlockingRunWithContext ever
// started the apiserver's own internal CRD-establishing controllers
// (EstablishingController et al.), so the controller the hook was waiting
// on was never running. ListenAndServe now starts those internal
// controllers first (see its own doc), so the hook can actually observe
// establishment.
func TestServerEndToEnd_WithPostStartHook_WaitsForCRDEstablishment(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-crd-establish.db")
	established := make(chan struct{})

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithPostStartHook(
			crdEstablishedHook("gadgets.libkapi.test", "libkapi.test", "gadgets", "gadget", "Gadget", established)),
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

	select {
	case <-established:
	case <-time.After(15 * time.Second):
		require.FailNow(t, "WithPostStartHook never observed CRD establishment - "+
			"the apiserver's internal CRD-establishing controllers likely never started before it ran")
	}

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")
}

// TestServerListenAndServe_PostStartHookErrorFailsStart verifies that a
// failing PostStartHookFunc surfaces as an ordinary error from
// ListenAndServe, instead of the klog.Fatal a failing
// k8s.io/apiserver-native PostStartHook would trigger (see
// PostStartHookFunc's doc).
func TestServerListenAndServe_PostStartHookErrorFailsStart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-poststart-error.db")

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithPostStartHook(func(_ context.Context, _ *restclient.Config) error {
			return errPostStartHookFailed
		}),
	)
	require.NoError(t, err)

	err = server.ListenAndServe(ctx)
	require.ErrorIs(t, err, errPostStartHookFailed)
}

// TestServerEndToEnd_WithPreShutdownHook verifies pre-shutdown hooks run in
// registration order, while the listener is still open, using the
// privileged loopback config - proven by successfully creating a real
// object from inside the hook itself, during Shutdown.
func TestServerEndToEnd_WithPreShutdownHook(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-preshutdown.db")
	tracker := &orderTracker{}

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithPreShutdownHook(configMapHook(tracker, "first", "pre-shutdown-hook-test")),
		libkapi.WithPreShutdownHook(orderOnlyHook(tracker, "second")),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	err = server.Shutdown(shutdownCtx)
	require.NoError(t, err)

	assert.Equal(t, []string{"first", "second"}, tracker.snapshot(), "pre-shutdown hooks must run in registration order")
}

// TestServerShutdown_DoesNotHangOnSlowPreShutdownHook is a regression test
// for the bound on Shutdown's wait for pre-shutdown hooks: even when a
// registered PreShutdownHookFunc ignores ctx cancellation for far longer
// than Shutdown's own ctx allows, Shutdown must still return once its
// deadline passes, rather than blocking until the hook actually finishes -
// the same guarantee TestServerShutdown_DoesNotHangOnSlowController proves
// for the controller manager.
func TestServerShutdown_DoesNotHangOnSlowPreShutdownHook(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-slow-preshutdown.db")

	stopped := make(chan struct{})

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithPreShutdownHook(func(hookCtx context.Context, _ *restclient.Config) error {
			<-hookCtx.Done()

			time.Sleep(slowShutdownDelay)

			close(stopped)

			return nil
		}),
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
	// deadline and surfaces it as an error - expected, not a bug, matching
	// TestServerShutdown_DoesNotHangOnSlowController.
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, shutdownElapsed, slowShutdownDelay,
		"Shutdown should return once its own ctx deadline passes, not wait for the slow pre-shutdown hook")

	select {
	case <-stopped:
		t.Fatal("expected the slow pre-shutdown hook to still be running when Shutdown returned")
	default:
	}

	select {
	case <-stopped:
	case <-time.After(slowShutdownDelay + time.Second):
		t.Fatal("slow pre-shutdown hook never actually stopped")
	}
}
