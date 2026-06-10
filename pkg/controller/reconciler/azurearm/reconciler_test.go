//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"context"
	"net/http"
	"testing"
	"time"

	resourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// fakeARM is a stub armRequester that records calls and returns canned responses.
type fakeARM struct {
	getResp  *armResponse
	getErr   error
	delResp  *armResponse
	delErr   error
	getCalls int
	delCalls int
}

func (f *fakeARM) get(_ context.Context, _ string, _ string) (*armResponse, error) {
	f.getCalls++

	return f.getResp, f.getErr
}

func (f *fakeARM) delete(_ context.Context, _ string, _ string) (*armResponse, error) {
	f.delCalls++

	return f.delResp, f.delErr
}

func (f *fakeARM) put(_ context.Context, _ string, _ string, _ any) (*armResponse, error) {
	return &armResponse{statusCode: http.StatusOK}, nil
}

// newManagedResourceGroup builds a ResourceGroup CR with metadata, our finalizer,
// and optional extra finalizers/annotations for delete-path tests.
func newManagedResourceGroup(finalizers []string, annos map[string]string) *resourcesv1.ResourceGroup {
	resourceGroup := newResourceGroup("my-rg")
	resourceGroup.ObjectMeta = metav1.ObjectMeta{
		Namespace:   testNamespace,
		Name:        "my-rg",
		Finalizers:  finalizers,
		Annotations: annos,
	}

	return resourceGroup
}

func newDeleteTestReconciler(
	t *testing.T,
	obj client.Object,
	gracePeriod time.Duration,
) *Reconciler {
	t.Helper()

	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(obj).
		WithStatusSubresource(&resourcesv1.ResourceGroup{}).
		Build()

	return &Reconciler{
		Client:              kubeClient,
		controllerName:      "azurearm-resourcegroup",
		newObj:              func() genruntime.ARMMetaObject { return &resourcesv1.ResourceGroup{} },
		armIDFor:            resourceGroupARMID,
		deletionGracePeriod: gracePeriod,
	}
}

func getResourceGroup(t *testing.T, reconciler *Reconciler) *resourcesv1.ResourceGroup {
	t.Helper()

	fetched := &resourcesv1.ResourceGroup{}
	key := types.NamespacedName{Namespace: testNamespace, Name: "my-rg"}

	err := reconciler.Get(context.Background(), key, fetched)
	if err != nil {
		t.Fatalf("getting resource group: %v", err)
	}

	return fetched
}

type ensureFinalizersCase struct {
	name         string
	policy       annotations.ReconcilePolicyValue
	finalizers   []string
	wantChanged  bool
	wantHasOurs  bool
	wantHasStale bool
}

func ensureFinalizersCases() []ensureFinalizersCase {
	return []ensureFinalizersCase{
		{
			name:        "manage adds our finalizer",
			policy:      annotations.ReconcilePolicyManage,
			finalizers:  nil,
			wantChanged: true,
			wantHasOurs: true,
		},
		{
			name:        "manage with our finalizer present is a no-op",
			policy:      annotations.ReconcilePolicyManage,
			finalizers:  []string{finalizerName},
			wantChanged: false,
			wantHasOurs: true,
		},
		{
			name:        "skip removes our finalizer",
			policy:      annotations.ReconcilePolicySkip,
			finalizers:  []string{finalizerName},
			wantChanged: true,
			wantHasOurs: false,
		},
		{
			name:        "detach-on-delete removes our finalizer",
			policy:      annotations.ReconcilePolicyDetachOnDelete,
			finalizers:  []string{finalizerName},
			wantChanged: true,
			wantHasOurs: false,
		},
		{
			name:         "manage strips stale ASO finalizer but keeps ours",
			policy:       annotations.ReconcilePolicyManage,
			finalizers:   []string{finalizerName, genruntime.ReconcilerFinalizer},
			wantChanged:  true,
			wantHasOurs:  true,
			wantHasStale: false,
		},
	}
}

func TestEnsureFinalizers(t *testing.T) {
	t.Parallel()

	for _, test := range ensureFinalizersCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resourceGroup := newManagedResourceGroup(test.finalizers, nil)
			reconciler := newDeleteTestReconciler(t, resourceGroup, 0)

			changed, err := reconciler.ensureFinalizers(
				context.Background(), resourceGroup, test.policy, zap.NewNop())
			if err != nil {
				t.Fatalf("ensureFinalizers returned error: %v", err)
			}

			if changed != test.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, test.wantChanged)
			}

			gotOurs := controllerutil.ContainsFinalizer(resourceGroup, finalizerName)
			if gotOurs != test.wantHasOurs {
				t.Fatalf("has our finalizer = %v, want %v", gotOurs, test.wantHasOurs)
			}

			gotStale := controllerutil.ContainsFinalizer(resourceGroup, genruntime.ReconcilerFinalizer)
			if gotStale != test.wantHasStale {
				t.Fatalf("has stale ASO finalizer = %v, want %v", gotStale, test.wantHasStale)
			}
		})
	}
}

func TestReconcileDeleteARMAlreadyGone(t *testing.T) {
	t.Parallel()

	resourceGroup := newManagedResourceGroup([]string{finalizerName}, nil)
	reconciler := newDeleteTestReconciler(t, resourceGroup, 15*time.Minute)
	arm := &fakeARM{getResp: &armResponse{statusCode: http.StatusNotFound}}
	creds := &azureCredentials{subscriptionID: testSubscriptionID, armClient: arm}

	_, err := reconciler.reconcileDeleteARM(context.Background(), resourceGroup, creds, testRGARMID)
	if err != nil {
		t.Fatalf("reconcileDeleteARM returned error: %v", err)
	}

	if arm.delCalls != 0 {
		t.Fatalf("expected no DELETE when resource already gone, got %d", arm.delCalls)
	}

	if controllerutil.ContainsFinalizer(resourceGroup, finalizerName) {
		t.Fatal("expected finalizer to be removed when resource already gone")
	}
}

func TestReconcileDeleteARMStillPresentIssuesDelete(t *testing.T) {
	t.Parallel()

	resourceGroup := newManagedResourceGroup([]string{finalizerName}, nil)
	reconciler := newDeleteTestReconciler(t, resourceGroup, 15*time.Minute)
	arm := &fakeARM{
		getResp: &armResponse{statusCode: http.StatusOK},
		delResp: &armResponse{statusCode: http.StatusAccepted},
	}
	creds := &azureCredentials{subscriptionID: testSubscriptionID, armClient: arm}

	result, err := reconciler.reconcileDeleteARM(context.Background(), resourceGroup, creds, testRGARMID)
	if err != nil {
		t.Fatalf("reconcileDeleteARM returned error: %v", err)
	}

	if arm.delCalls != 1 {
		t.Fatalf("expected exactly one DELETE, got %d", arm.delCalls)
	}

	if result.RequeueAfter <= 0 {
		t.Fatalf("expected a requeue while deletion is in flight, got %v", result.RequeueAfter)
	}

	persisted := getResourceGroup(t, reconciler)
	if !controllerutil.ContainsFinalizer(persisted, finalizerName) {
		t.Fatal("expected finalizer to remain while deletion is in flight")
	}

	if persisted.GetAnnotations()[deletionStartedAnnotation] == "" {
		t.Fatal("expected deletion-started annotation to be stamped")
	}
}

func TestReconcileDeleteARMGraceExpiredReleasesFinalizer(t *testing.T) {
	t.Parallel()

	startedLongAgo := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	resourceGroup := newManagedResourceGroup(
		[]string{finalizerName},
		map[string]string{deletionStartedAnnotation: startedLongAgo},
	)
	reconciler := newDeleteTestReconciler(t, resourceGroup, 15*time.Minute)
	arm := &fakeARM{getResp: &armResponse{statusCode: http.StatusOK}}
	creds := &azureCredentials{subscriptionID: testSubscriptionID, armClient: arm}

	_, err := reconciler.reconcileDeleteARM(context.Background(), resourceGroup, creds, testRGARMID)
	if err != nil {
		t.Fatalf("reconcileDeleteARM returned error: %v", err)
	}

	if arm.delCalls != 0 {
		t.Fatalf("expected no DELETE once grace period expired, got %d", arm.delCalls)
	}

	if controllerutil.ContainsFinalizer(resourceGroup, finalizerName) {
		t.Fatal("expected finalizer to be released after grace period expired")
	}
}

func TestReconcileDeleteARMRateLimitedGet(t *testing.T) {
	t.Parallel()

	resourceGroup := newManagedResourceGroup([]string{finalizerName}, nil)
	reconciler := newDeleteTestReconciler(t, resourceGroup, 15*time.Minute)
	arm := &fakeARM{getResp: &armResponse{
		statusCode: http.StatusTooManyRequests,
		retryAfter: 30 * time.Second,
	}}
	creds := &azureCredentials{subscriptionID: testSubscriptionID, armClient: arm}

	result, err := reconciler.reconcileDeleteARM(context.Background(), resourceGroup, creds, testRGARMID)
	if err != nil {
		t.Fatalf("reconcileDeleteARM returned error: %v", err)
	}

	if result.RequeueAfter != 30*time.Second {
		t.Fatalf("expected requeue after 30s on rate limit, got %v", result.RequeueAfter)
	}

	if arm.delCalls != 0 {
		t.Fatalf("expected no DELETE while rate limited, got %d", arm.delCalls)
	}
}
