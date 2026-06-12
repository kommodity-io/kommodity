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

// TestIsTerminalARMError pins the classification that decides whether a failed ARM
// PUT is fatal (mark the resource Failed, stop retrying) or retryable. A
// misclassification here is high-consequence: treating a 429/transient code as
// terminal strands a resource as Failed, while treating a 400 as retryable spins
// the controller forever on an unfixable spec.
func TestIsTerminalARMError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"200 OK is not terminal", http.StatusOK, false},
		{"400 Bad Request is terminal", http.StatusBadRequest, true},
		{"401 Unauthorized is terminal", http.StatusUnauthorized, true},
		{"403 Forbidden is terminal", http.StatusForbidden, true},
		{"404 Not Found is not terminal (handled separately)", http.StatusNotFound, false},
		{"409 Conflict is not terminal (handled separately)", http.StatusConflict, false},
		{"422 Unprocessable Entity is terminal", http.StatusUnprocessableEntity, true},
		{"429 Too Many Requests is not terminal (rate limited)", http.StatusTooManyRequests, false},
		{"499 client-range edge is terminal", 499, true},
		{"500 Internal Server Error is not terminal", http.StatusInternalServerError, false},
		{"503 Service Unavailable is not terminal", http.StatusServiceUnavailable, false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := isTerminalARMError(test.statusCode)
			if got != test.want {
				t.Fatalf("isTerminalARMError(%d) = %v, want %v", test.statusCode, got, test.want)
			}
		})
	}
}

// TestReconcilePolicyFor pins the reconcile-policy resolution that drives whether
// the reconciler carries a finalizer and deletes the resource in Azure. The
// absent-annotation default must be "manage" (matching ASO); a wrong default is
// exactly the failure mode behind orphaned resources / stuck finalizers.
func TestReconcilePolicyFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		annotations map[string]string
		want        annotations.ReconcilePolicyValue
	}{
		{"nil annotations default to manage", nil, annotations.ReconcilePolicyManage},
		{"empty annotations default to manage", map[string]string{}, annotations.ReconcilePolicyManage},
		{
			"empty policy value defaults to manage",
			map[string]string{annotations.ReconcilePolicy: ""},
			annotations.ReconcilePolicyManage,
		},
		{
			"explicit manage is honoured",
			map[string]string{annotations.ReconcilePolicy: string(annotations.ReconcilePolicyManage)},
			annotations.ReconcilePolicyManage,
		},
		{
			"explicit skip is honoured",
			map[string]string{annotations.ReconcilePolicy: string(annotations.ReconcilePolicySkip)},
			annotations.ReconcilePolicySkip,
		},
		{
			"explicit detach-on-delete is honoured",
			map[string]string{annotations.ReconcilePolicy: string(annotations.ReconcilePolicyDetachOnDelete)},
			annotations.ReconcilePolicyDetachOnDelete,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resourceGroup := newResourceGroup("my-rg")
			resourceGroup.ObjectMeta = metav1.ObjectMeta{Annotations: test.annotations}

			got := reconcilePolicyFor(resourceGroup)
			if got != test.want {
				t.Fatalf("reconcilePolicyFor() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestParseProvisioningState pins extraction of properties.provisioningState from
// an ARM response body — the value that drives whether a resource is reported
// Ready or requeued. A malformed or empty body must degrade to "" (not crash, not
// a false "Succeeded"), and a missing field must not be mistaken for a state.
func TestParseProvisioningState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty body yields empty state", "", ""},
		{"malformed JSON yields empty state", "{not valid json", ""},
		{"succeeded state is extracted", `{"properties":{"provisioningState":"Succeeded"}}`, "Succeeded"},
		{"in-progress state is extracted", `{"properties":{"provisioningState":"Updating"}}`, "Updating"},
		{"properties without provisioningState yields empty", `{"properties":{"foo":"bar"}}`, ""},
		{"body without properties yields empty", `{"id":"/subscriptions/x"}`, ""},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := parseProvisioningState([]byte(test.body))
			if got != test.want {
				t.Fatalf("parseProvisioningState(%q) = %q, want %q", test.body, got, test.want)
			}
		})
	}
}
