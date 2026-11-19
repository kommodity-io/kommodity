//nolint:testpackage // white-box tests exercise unexported fixer internals
package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/controller/reconciler/azurearm"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testFixerNamespace      = "default"
	testFixerCluster        = "wachs-ha"
	testFixerRG             = "wachs-ha"
	testFixerSub            = "sub-123"
	testFixerTenant         = "tenant-123"
	testFixerClientID       = "client-123"
	testFixerIdentity       = "aci"
	testFixerIdentitySecret = "azure-cluster-identity-secret"
	testFixerSecretPW       = "super-secret-pw"
	// testIdentityRefKind is the Kind of the AzureClusterIdentity reference.
	testIdentityRefKind = "AzureClusterIdentity"
)

// fakeFixerARM is a stub azurearm.GenericARMClient that records calls and
// returns canned responses keyed by whether the URL is the LB or a probe
// sub-resource.
type fakeFixerARM struct {
	getLBStatus    int
	getLBBody      []byte
	getErr         error
	putStatus      int
	getCalls       int
	putCalls       int
	lastPutARMID   string
	lastPutAPIVer  string
	lastPutIfMatch string
	lastPutBody    []byte
}

func (f *fakeFixerARM) Get(
	_ context.Context,
	_ string,
	_ string,
) (int, []byte, time.Duration, error) {
	f.getCalls++

	if f.getErr != nil {
		return 0, nil, 0, f.getErr
	}

	if f.getLBStatus == 0 {
		f.getLBStatus = http.StatusOK
	}

	retryAfter := time.Duration(0)
	if f.getLBStatus == http.StatusTooManyRequests {
		retryAfter = 30 * time.Second
	}

	return f.getLBStatus, f.getLBBody, retryAfter, nil
}

func (f *fakeFixerARM) Put(
	_ context.Context,
	armID string,
	apiVersion string,
	ifMatch string,
	payload any,
) (int, []byte, time.Duration, error) {
	f.putCalls++
	f.lastPutARMID = armID
	f.lastPutAPIVer = apiVersion
	f.lastPutIfMatch = ifMatch

	if payload != nil {
		data, ok := payload.([]byte)
		if !ok {
			var err error

			data, err = json.Marshal(payload)
			if err != nil {
				return 0, nil, 0, fmt.Errorf("marshaling put body: %w", err)
			}
		}

		f.lastPutBody = data
	}

	status := f.putStatus
	if status == 0 {
		status = http.StatusOK
	}

	return status, nil, 0, nil
}

func fixerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()

	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, clusterv1.AddToScheme, infrav1.AddToScheme,
	} {
		err := add(scheme)
		if err != nil {
			t.Fatalf("adding to scheme: %v", err)
		}
	}

	return scheme
}

type fixerFixture struct {
	withIdentity     bool
	withClientSecret bool
	withIdentityRef  bool
	withOwnerCluster bool
	ownerPaused      bool
	selfPaused       bool
	identityAllowed  *infrav1.AllowedNamespaces
	lbType           infrav1.LBType
}

func buildFixer(t *testing.T, fixture fixerFixture, arm azurearm.GenericARMClient) *AzureLBProbeFixer {
	t.Helper()

	azureCluster, ownerCluster := newFixerClusterObjects(fixture)
	objs := newFixerIdentityObjects(fixture, azureCluster, ownerCluster)

	c := fake.NewClientBuilder().WithScheme(fixerScheme(t)).WithObjects(objs...).Build()

	return &AzureLBProbeFixer{
		Client:    c,
		armClient: func(string, string, string) (azurearm.GenericARMClient, error) { return arm, nil },
	}
}

// newFixerClusterObjects builds the fixture's AzureCluster and, when the
// fixture asks for one, its owning CAPI Cluster with the pause state under
// test.
func newFixerClusterObjects(
	fixture fixerFixture,
) (*infrav1.AzureCluster, *clusterv1.Cluster) {
	azureCluster := &infrav1.AzureCluster{}
	azureCluster.Name = testFixerCluster
	azureCluster.Namespace = testFixerNamespace
	azureCluster.Spec = infrav1.AzureClusterSpec{
		SubscriptionID: testFixerSub,
		Location:       testLocation,
		ResourceGroup:  testFixerRG,
		NetworkSpec: infrav1.NetworkSpec{
			APIServerLB: &infrav1.LoadBalancerSpec{
				Name: testFixerCluster + "-public-lb",
				Type: fixture.lbType,
			},
		},
	}

	if fixture.withIdentityRef {
		azureCluster.Spec.IdentityRef = &corev1.ObjectReference{
			Kind: testIdentityRefKind, Name: testFixerIdentity, Namespace: testFixerNamespace,
		}
	}

	if fixture.selfPaused {
		azureCluster.Annotations = map[string]string{
			clusterv1.PausedAnnotation: "true",
		}
	}

	if !fixture.withOwnerCluster {
		return azureCluster, nil
	}

	cluster := &clusterv1.Cluster{}
	cluster.Name = testFixerCluster
	cluster.Namespace = testFixerNamespace
	cluster.Spec.Paused = fixture.ownerPaused
	cluster.SetUID("test-owner-uid")

	azureCluster.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: clusterv1.GroupVersion.String(),
		Kind:       "Cluster",
		Name:       cluster.Name,
		UID:        cluster.UID,
	}})

	return azureCluster, cluster
}

// newFixerIdentityObjects assembles the fake client's object set: the
// AzureCluster, the owning Cluster when present, and the identity chain
// (identity + clientSecret) the fixture enables.
func newFixerIdentityObjects(
	fixture fixerFixture,
	azureCluster *infrav1.AzureCluster,
	ownerCluster *clusterv1.Cluster,
) []client.Object {
	objs := []client.Object{azureCluster}

	if ownerCluster != nil {
		objs = append(objs, ownerCluster)
	}

	identity := &infrav1.AzureClusterIdentity{}
	identity.Name = testFixerIdentity
	identity.Namespace = testFixerNamespace
	identity.Spec = infrav1.AzureClusterIdentitySpec{
		Type:         infrav1.ServicePrincipal,
		TenantID:     testFixerTenant,
		ClientID:     testFixerClientID,
		ClientSecret: corev1.SecretReference{Name: testFixerIdentitySecret, Namespace: testFixerNamespace},
	}

	// Mirror the identity the live clusters use: an empty AllowedNamespaces
	// object allows every namespace, while nil denies all (CRD semantics).
	if fixture.identityAllowed != nil {
		identity.Spec.AllowedNamespaces = fixture.identityAllowed
	} else {
		identity.Spec.AllowedNamespaces = &infrav1.AllowedNamespaces{}
	}

	if fixture.withIdentity {
		objs = append(objs, identity)
	}

	if fixture.withClientSecret {
		identitySecret := &corev1.Secret{}
		identitySecret.Name = testFixerIdentitySecret
		identitySecret.Namespace = testFixerNamespace
		identitySecret.Data = map[string][]byte{lbProbeFixIdentityClientSecretKey: []byte(testFixerSecretPW)}

		objs = append(objs, identitySecret)
	}

	return objs
}

func fixerReconcile(t *testing.T, fixer *AzureLBProbeFixer) ctrl.Result {
	t.Helper()

	result, err := fixer.Reconcile(context.Background(), ctrl.Request{
		Namespace: testFixerNamespace, Name: testFixerCluster,
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	return result
}

// testProbePort is the apiserver port used across probe fixtures.
const testProbePort = 6443

// JSON keys shared by the LB fixtures below.
const (
	propsKeyJSON  = "properties"
	lbKeyLocation = "location"
	lbKeyProbes   = "probes"
	lbKeyName     = "name"
	lbKeyProtocol = "protocol"
	lbKeyPort     = "port"
)

// lbGetBody renders an ARM LB GET response with a single probe of the given
// shape, using CAPZ's default interval and threshold.
func lbGetBody(protocol string, requestPath string) []byte {
	const propsKey = propsKeyJSON

	body := map[string]any{
		"etag":        `W/"test-etag"`,
		lbKeyLocation: testLocation,
		propsKey: map[string]any{
			lbKeyProbes: []any{
				map[string]any{
					lbKeyName: lbProbeName,
					propsKey: map[string]any{
						lbKeyProtocol:       protocol,
						lbKeyPort:           testProbePort,
						"requestPath":       requestPath,
						"intervalInSeconds": lbProbeDefaultIntervalSeconds,
						"probeThreshold":    lbProbeDefaultThreshold,
					},
				},
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("marshaling LB fixture: %v", err))
	}

	return data
}

func TestFixerConvertsHTTPSProbeToTCP(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody: lbGetBody("Https", "/readyz"),
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	result := fixerReconcile(t, fixer)

	if result.RequeueAfter != lbProbeFixInterval {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, lbProbeFixInterval)
	}

	if arm.putCalls != 1 {
		t.Fatalf("putCalls = %d, want 1", arm.putCalls)
	}

	wantID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers/%s-public-lb",
		testFixerSub, testFixerRG, testFixerCluster,
	)

	if arm.lastPutARMID != wantID {
		t.Errorf("put ARM ID = %q, want the LB resource itself %q", arm.lastPutARMID, wantID)
	}

	if arm.lastPutAPIVer != lbProbeAPIVersion {
		t.Errorf("put api-version = %q, want %q", arm.lastPutAPIVer, lbProbeAPIVersion)
	}

	if arm.lastPutIfMatch != `W/"test-etag"` {
		t.Errorf("put If-Match = %q, want the LB's etag", arm.lastPutIfMatch)
	}

	putProbeProps := assertPutProbe(t, arm)

	if _, hasPath := putProbeProps["requestPath"]; hasPath {
		t.Error("put probe still carries requestPath; TCP probes must not have one")
	}

	for key, want := range map[string]any{
		lbKeyProtocol:       lbProbeProtocolTCP,
		lbKeyPort:           float64(testProbePort),
		"intervalInSeconds": float64(lbProbeDefaultIntervalSeconds),
		"probeThreshold":    float64(lbProbeDefaultThreshold),
	} {
		if got := putProbeProps[key]; got != want {
			t.Errorf("put probe %s = %v, want %v", key, got, want)
		}
	}
}

// assertPutProbe unmarshals the fake's recorded PUT body and returns the
// properties of the single probe in it.
func assertPutProbe(t *testing.T, arm *fakeFixerARM) map[string]any {
	t.Helper()

	var got map[string]any

	err := json.Unmarshal(arm.lastPutBody, &got)
	if err != nil {
		t.Fatalf("put body is not valid JSON: %v", err)
	}

	props, hasProps := got[propsKeyJSON].(map[string]any)
	if !hasProps {
		t.Fatal("put body has no properties object")
	}

	probes, hasProbes := props[lbKeyProbes].([]any)
	if !hasProbes || len(probes) != 1 {
		t.Fatalf("put body has %d probes, want exactly 1", len(probes))
	}

	entry, isMap := probes[0].(map[string]any)
	if !isMap {
		t.Fatal("put probe entry is not an object")
	}

	probeProps, hasEntryProps := entry[propsKeyJSON].(map[string]any)
	if !hasEntryProps {
		t.Fatal("put probe entry has no properties object")
	}

	return probeProps
}

// lbBodyWithRules renders an ARM LB GET body carrying the HTTPS probe plus
// load balancing rules and a backend pool, to verify they survive the
// read-modify-write.
func lbBodyWithRules() []byte {
	const lbName = testFixerCluster + "-public-lb"

	lbBody := map[string]any{
		"etag":        `W/"abc-123"`,
		lbKeyLocation: testLocation,
		propsKeyJSON: map[string]any{
			lbKeyProbes: []any{
				map[string]any{
					lbKeyName: lbProbeName,
					propsKeyJSON: map[string]any{
						lbKeyProtocol: "Https",
						lbKeyPort:     testProbePort,
						"requestPath": "/readyz",
					},
				},
			},
			"loadBalancingRules": []any{
				map[string]any{
					lbKeyName:    lbName + "-HTTPSRule",
					propsKeyJSON: map[string]any{"backendPort": testProbePort},
				},
			},
			"backendAddressPools": []any{
				map[string]any{lbKeyName: lbName + "-backend"},
			},
		},
	}

	data, err := json.Marshal(lbBody)
	if err != nil {
		panic(fmt.Sprintf("marshaling LB fixture: %v", err))
	}

	return data
}

func TestFixerPutPreservesUnrelatedLBFields(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{getLBBody: lbBodyWithRules()}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	fixerReconcile(t, fixer)

	if arm.putCalls != 1 {
		t.Fatalf("putCalls = %d, want 1", arm.putCalls)
	}

	if arm.lastPutIfMatch != `W/"abc-123"` {
		t.Errorf("If-Match = %q, want the LB etag", arm.lastPutIfMatch)
	}

	var got map[string]any

	err := json.Unmarshal(arm.lastPutBody, &got)
	if err != nil {
		t.Fatalf("put body is not valid JSON: %v", err)
	}

	props, hasProps := got[propsKeyJSON].(map[string]any)
	if !hasProps {
		t.Fatal("put body has no properties object")
	}

	rules, hasRules := props["loadBalancingRules"].([]any)
	if !hasRules || len(rules) != 1 {
		t.Error("load balancing rules did not survive the read-modify-write")
	}

	pools, hasPools := props["backendAddressPools"].([]any)
	if !hasPools || len(pools) != 1 {
		t.Error("backend address pools did not survive the read-modify-write")
	}

	if got[lbKeyLocation] != testLocation {
		t.Error("location did not survive the read-modify-write")
	}
}

func TestFixerNoOpWhenAlreadyTCP(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody: lbGetBody("Tcp", ""),
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	fixerReconcile(t, fixer)

	if arm.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (probe already TCP)", arm.putCalls)
	}
}

func TestFixerNoOpWhenUserModifiedPath(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody: lbGetBody("Https", "/healthz"),
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	fixerReconcile(t, fixer)

	if arm.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (user-modified request path left alone)", arm.putCalls)
	}
}

func TestFixerNoOpWhenProbeAbsent(t *testing.T) {
	t.Parallel()

	body := map[string]any{
		propsKeyJSON: map[string]any{lbKeyProbes: []any{}},
	}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}

	arm := &fakeFixerARM{getLBBody: data}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	fixerReconcile(t, fixer)

	if arm.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (no probe present)", arm.putCalls)
	}
}

func TestFixerRetriesWhenLBMissing(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{getLBStatus: http.StatusNotFound}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	result := fixerReconcile(t, fixer)

	if arm.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (LB missing)", arm.putCalls)
	}

	if result.RequeueAfter != lbProbeFixInterval {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, lbProbeFixInterval)
	}
}

func TestFixerRequeuesWhenCredentialsPending(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		fixture fixerFixture
	}{
		{
			name:    "identity missing",
			fixture: fixerFixture{withIdentity: false, withClientSecret: true, withIdentityRef: true},
		},
		{
			name:    "client secret missing",
			fixture: fixerFixture{withIdentity: true, withClientSecret: false, withIdentityRef: true},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			arm := &fakeFixerARM{}
			fixer := buildFixer(t, testCase.fixture, arm)

			result := fixerReconcile(t, fixer)

			if arm.getCalls != 0 {
				t.Errorf("getCalls = %d, want 0 (no ARM call without credentials)", arm.getCalls)
			}

			if result.RequeueAfter != RequeueAfter {
				t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, RequeueAfter)
			}
		})
	}
}

func TestFixerSkipsNonServicePrincipalIdentity(t *testing.T) {
	t.Parallel()

	azureCluster := &infrav1.AzureCluster{}
	azureCluster.Name = testFixerCluster
	azureCluster.Namespace = testFixerNamespace
	azureCluster.Spec = infrav1.AzureClusterSpec{
		SubscriptionID: testFixerSub,
		Location:       testLocation,
		IdentityRef: &corev1.ObjectReference{
			Kind: testIdentityRefKind, Name: testFixerIdentity, Namespace: testFixerNamespace,
		},
		ResourceGroup: testFixerRG,
	}

	msiIdentity := &infrav1.AzureClusterIdentity{}
	msiIdentity.Name = testFixerIdentity
	msiIdentity.Namespace = testFixerNamespace
	msiIdentity.Spec = infrav1.AzureClusterIdentitySpec{
		Type:         infrav1.UserAssignedMSI,
		TenantID:     testFixerTenant,
		ClientID:     testFixerClientID,
		ClientSecret: corev1.SecretReference{Name: testFixerIdentitySecret, Namespace: testFixerNamespace},
	}

	identitySecret := &corev1.Secret{}
	identitySecret.Name = testFixerIdentitySecret
	identitySecret.Namespace = testFixerNamespace
	identitySecret.Data = map[string][]byte{lbProbeFixIdentityClientSecretKey: []byte(testFixerSecretPW)}

	objs := []client.Object{azureCluster, msiIdentity, identitySecret}

	c := fake.NewClientBuilder().WithScheme(fixerScheme(t)).WithObjects(objs...).Build()

	arm := &fakeFixerARM{}
	fixer := &AzureLBProbeFixer{
		Client: c,
		armClient: func(string, string, string) (azurearm.GenericARMClient, error) {
			return arm, nil
		},
	}

	result, err := fixer.Reconcile(context.Background(), ctrl.Request{
		Namespace: testFixerNamespace, Name: testFixerCluster,
	})
	if err != nil {
		t.Fatalf("Reconcile returned error for non-ServicePrincipal identity: %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 (observe-only for non-ServicePrincipal)", result.RequeueAfter)
	}

	if arm.getCalls != 0 {
		t.Errorf("getCalls = %d, want 0 (no ARM calls for non-ServicePrincipal identity)", arm.getCalls)
	}
}

func TestFixerRequeuesWhenPutRateLimited(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody: lbGetBody("Https", "/readyz"),
		putStatus: http.StatusTooManyRequests,
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	result := fixerReconcile(t, fixer)

	if result.RequeueAfter <= 0 {
		t.Errorf("RequeueAfter = %v, want positive rate-limit backoff", result.RequeueAfter)
	}
}

func TestFixerRequeuesSoonAfterPreconditionFailure(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody: lbGetBody("Https", "/readyz"),
		putStatus: http.StatusPreconditionFailed,
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	result := fixerReconcile(t, fixer)

	// Contention must retry on the short cadence, not the drift-guard one:
	// the HTTPS probe stays in place until the retry, and the drift interval
	// exceeds the MachineHealthCheck node startup timeout.
	if result.RequeueAfter != RequeueAfter {
		t.Errorf("RequeueAfter = %v, want short retry %v (stale etag)", result.RequeueAfter, RequeueAfter)
	}
}

func TestFixerSkipsDeniedNamespaceIdentity(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{getLBBody: lbGetBody("Https", "/readyz")}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
		identityAllowed: &infrav1.AllowedNamespaces{NamespaceList: []string{"somewhere-else"}},
	}, arm)

	result := fixerReconcile(t, fixer)

	if arm.getCalls != 0 {
		t.Errorf("getCalls = %d, want 0 (no ARM call from a denied namespace)", arm.getCalls)
	}

	if arm.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (no LB write from a denied namespace)", arm.putCalls)
	}

	if result.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 (observe-only for a denied namespace)", result.RequeueAfter)
	}
}

func TestFixerRespectsPausedCluster(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		fixture fixerFixture
		wantGet int
	}{
		{
			name:    "paused annotation on AzureCluster",
			fixture: fixerFixture{withIdentity: true, withClientSecret: true, withIdentityRef: true, selfPaused: true},
			wantGet: 0,
		},
		{
			name: "paused owner Cluster",
			fixture: fixerFixture{
				withIdentity: true, withClientSecret: true, withIdentityRef: true,
				withOwnerCluster: true, ownerPaused: true,
			},
			wantGet: 0,
		},
		{
			name: "unpaused owner Cluster still fixes",
			fixture: fixerFixture{
				withIdentity: true, withClientSecret: true, withIdentityRef: true,
				withOwnerCluster: true, ownerPaused: false,
			},
			wantGet: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			arm := &fakeFixerARM{getLBBody: lbGetBody("Https", "/readyz")}
			fixer := buildFixer(t, testCase.fixture, arm)

			result := fixerReconcile(t, fixer)

			if arm.getCalls != testCase.wantGet {
				t.Errorf("getCalls = %d, want %d", arm.getCalls, testCase.wantGet)
			}

			// Paused or not, the drift-guard cadence applies either way.
			if result.RequeueAfter != lbProbeFixInterval {
				t.Errorf("RequeueAfter = %v, want drift-guard %v", result.RequeueAfter, lbProbeFixInterval)
			}
		})
	}
}

func TestFixerNameDerivation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		lbSpec   *infrav1.LoadBalancerSpec
		cluster  string
		expected string
	}{
		{
			name:     "explicit name wins",
			lbSpec:   &infrav1.LoadBalancerSpec{Name: "custom-lb", Type: infrav1.Public},
			cluster:  "c1",
			expected: "custom-lb",
		},
		{
			name:     "internal default",
			lbSpec:   &infrav1.LoadBalancerSpec{Type: infrav1.Internal},
			cluster:  "c1",
			expected: "c1-internal-lb",
		},
		{
			name:     "public default",
			lbSpec:   &infrav1.LoadBalancerSpec{Type: infrav1.Public},
			cluster:  "c1",
			expected: "c1-public-lb",
		},
		{
			name:     "nil spec defaults public",
			lbSpec:   nil,
			cluster:  "c1",
			expected: "c1-public-lb",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			azureCluster := &infrav1.AzureCluster{}
			azureCluster.Name = testCase.cluster
			azureCluster.Namespace = testFixerNamespace
			azureCluster.Spec.NetworkSpec = infrav1.NetworkSpec{APIServerLB: testCase.lbSpec}

			got := apiserverLBName(azureCluster)
			if got != testCase.expected {
				t.Errorf("apiserverLBName = %q, want %q", got, testCase.expected)
			}
		})
	}
}

func TestFixerRateLimitedRequeue(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{
		getLBBody:   lbGetBody("Https", "/readyz"),
		getLBStatus: http.StatusTooManyRequests,
	}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	result := fixerReconcile(t, fixer)

	if result.RequeueAfter <= 0 {
		t.Errorf("RequeueAfter = %v, want positive rate-limit backoff", result.RequeueAfter)
	}
}

func TestFixerTerminalGetError(t *testing.T) {
	t.Parallel()

	arm := &fakeFixerARM{getLBStatus: http.StatusForbidden}
	fixer := buildFixer(t, fixerFixture{
		withIdentity: true, withClientSecret: true, withIdentityRef: true,
	}, arm)

	_, err := fixer.Reconcile(context.Background(), ctrl.Request{
		Namespace: testFixerNamespace, Name: testFixerCluster,
	})
	if err == nil {
		t.Fatal("Reconcile returned nil error, want terminal ARM error")
	}

	if !errors.Is(err, azurearm.ErrARMTerminal) {
		t.Errorf("error = %v, want wrap of azurearm.ErrARMTerminal", err)
	}
}
