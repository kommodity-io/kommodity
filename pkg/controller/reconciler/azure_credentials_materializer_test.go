//nolint:testpackage // white-box tests exercise unexported materializer internals
package reconciler

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testMatNamespace = "default"
	testClusterName  = "wachs-ha"
	//nolint:gosec // G101: test secret resource name, not a credential
	testCCMSecretName  = "wachs-ha-cloud-provider"
	testIdentitySecret = "azure-cluster-identity-secret"
	testClientSecretPW = "super-secret-pw"
	testSubscription   = "sub-123"
	testTenant         = "tenant-123"
	testClientID       = "client-123"
	testRG             = "wachs-ha"
	testLocation       = "westeurope"
	testVnetName       = "wachs-ha-vnet"
)

func materializerScheme(t *testing.T) *runtime.Scheme {
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

type materializerFixture struct {
	identityType      infrav1.IdentityType
	withClientSecret  bool
	ccmEnabled        bool
	preExistingSecret *corev1.Secret
}

func buildMaterializer(t *testing.T, fixture materializerFixture) *AzureCredentialMaterializer {
	t.Helper()

	objs := []client.Object{
		&infrav1.AzureCluster{
			ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: testMatNamespace},
			Spec: infrav1.AzureClusterSpec{
				AzureClusterClassSpec: infrav1.AzureClusterClassSpec{
					SubscriptionID: testSubscription,
					Location:       testLocation,
					IdentityRef: &corev1.ObjectReference{
						Kind: "AzureClusterIdentity", Name: "aci", Namespace: testMatNamespace,
					},
				},
				ResourceGroup: testRG,
				NetworkSpec: infrav1.NetworkSpec{
					Vnet: infrav1.VnetSpec{Name: testVnetName, ResourceGroup: testRG},
				},
			},
		},
		&infrav1.AzureClusterIdentity{
			ObjectMeta: metav1.ObjectMeta{Name: "aci", Namespace: testMatNamespace},
			Spec: infrav1.AzureClusterIdentitySpec{
				Type:         fixture.identityType,
				TenantID:     testTenant,
				ClientID:     testClientID,
				ClientSecret: corev1.SecretReference{Name: testIdentitySecret, Namespace: testMatNamespace},
			},
		},
	}

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: testMatNamespace},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: &corev1.ObjectReference{Kind: azureInfraKind, Name: testClusterName},
		},
	}

	if fixture.ccmEnabled {
		cluster.Annotations = map[string]string{AnnotationCCMSecretName: testCCMSecretName}
	}

	objs = append(objs, cluster)

	if fixture.withClientSecret {
		objs = append(objs, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testIdentitySecret, Namespace: testMatNamespace},
			Data:       map[string][]byte{identityClientSecretKey: []byte(testClientSecretPW)},
		})
	}

	if fixture.preExistingSecret != nil {
		objs = append(objs, fixture.preExistingSecret)
	}

	c := fake.NewClientBuilder().WithScheme(materializerScheme(t)).WithObjects(objs...).Build()

	return &AzureCredentialMaterializer{Client: c}
}

func reconcileCluster(t *testing.T, materializer *AzureCredentialMaterializer) ctrl.Result {
	t.Helper()

	result, err := materializer.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testMatNamespace, Name: testClusterName},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	return result
}

func getSecret(t *testing.T, materializer *AzureCredentialMaterializer, name string) *corev1.Secret {
	t.Helper()

	secret := &corev1.Secret{}

	err := materializer.Get(context.Background(), types.NamespacedName{Namespace: testMatNamespace, Name: name}, secret)
	if err != nil {
		t.Fatalf("getting secret %s: %v", name, err)
	}

	return secret
}

func TestMaterializeBothSecrets(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: true,
	})
	reconcileCluster(t, materializer)

	aso := getSecret(t, materializer, testClusterName+asoSecretSuffix)

	wantASO := map[string]string{
		"AZURE_SUBSCRIPTION_ID": testSubscription,
		"AZURE_TENANT_ID":       testTenant,
		"AZURE_CLIENT_ID":       testClientID,
		"AZURE_CLIENT_SECRET":   testClientSecretPW,
	}
	for k, want := range wantASO {
		if got := string(aso.Data[k]); got != want {
			t.Errorf("aso-secret[%s] = %q, want %q", k, got, want)
		}
	}

	if aso.Labels[materializerManagedByLabel] != managedByLabelValue {
		t.Error("aso-secret missing materializer managed-by label")
	}

	if len(aso.OwnerReferences) != 1 || aso.OwnerReferences[0].Name != testClusterName {
		t.Errorf("aso-secret ownerReferences = %+v, want owner Cluster %s", aso.OwnerReferences, testClusterName)
	}

	ccm := getSecret(t, materializer, testCCMSecretName)

	// Cloud-config content is asserted exhaustively by
	// TestBuildAzureCloudConfigContract; here we only confirm the materializer
	// wrote a parseable payload under the expected key.
	var cfg map[string]any

	err := json.Unmarshal(ccm.Data[ccmCloudConfigKey], &cfg)
	if err != nil {
		t.Fatalf("cloud-config is not valid JSON: %v", err)
	}

	if len(cfg) == 0 {
		t.Error("materialized cloud-config is empty")
	}
}

func TestMaterializeRequeuesWithoutClientSecret(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: false, ccmEnabled: true,
	})

	result := reconcileCluster(t, materializer)
	if result.RequeueAfter <= 0 {
		t.Fatalf("expected requeue while clientSecret Secret is absent, got %v", result.RequeueAfter)
	}

	secret := &corev1.Secret{}

	err := materializer.Get(context.Background(),
		types.NamespacedName{Namespace: testMatNamespace, Name: testClusterName + asoSecretSuffix}, secret)
	if err == nil {
		t.Fatal("aso-secret should not be created before the clientSecret Secret exists")
	}
}

func TestMaterializeSkipsNonServicePrincipal(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.UserAssignedMSI, withClientSecret: true, ccmEnabled: true,
	})
	reconcileCluster(t, materializer)

	secret := &corev1.Secret{}

	err := materializer.Get(context.Background(),
		types.NamespacedName{Namespace: testMatNamespace, Name: testClusterName + asoSecretSuffix}, secret)
	if err == nil {
		t.Fatal("non-ServicePrincipal identity should not materialize an aso-secret")
	}
}

func TestMaterializeDoesNotClobberOperatorSecret(t *testing.T) {
	t.Parallel()

	operatorSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName + asoSecretSuffix, Namespace: testMatNamespace},
		Data:       map[string][]byte{"AZURE_CLIENT_SECRET": []byte("operator-managed")},
	}

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: false,
		preExistingSecret: operatorSecret,
	})
	reconcileCluster(t, materializer)

	aso := getSecret(t, materializer, testClusterName+asoSecretSuffix)
	if string(aso.Data["AZURE_CLIENT_SECRET"]) != "operator-managed" {
		t.Errorf("operator-supplied secret was clobbered: %q", string(aso.Data["AZURE_CLIENT_SECRET"]))
	}
}

// TestMaterializePopulatesEmptyPlaceholderSecret verifies the escape hatch is gated
// on the Secret carrying data: an empty, unlabeled placeholder Secret is not treated
// as operator-supplied and gets populated (rather than left empty forever).
func TestMaterializePopulatesEmptyPlaceholderSecret(t *testing.T) {
	t.Parallel()

	emptySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName + asoSecretSuffix, Namespace: testMatNamespace},
	}

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: false,
		preExistingSecret: emptySecret,
	})
	reconcileCluster(t, materializer)

	aso := getSecret(t, materializer, testClusterName+asoSecretSuffix)
	if string(aso.Data["AZURE_CLIENT_SECRET"]) != testClientSecretPW {
		t.Errorf("empty placeholder secret was not populated: AZURE_CLIENT_SECRET = %q",
			string(aso.Data["AZURE_CLIENT_SECRET"]))
	}

	if aso.Labels[materializerManagedByLabel] != managedByLabelValue {
		t.Error("populated placeholder secret missing materializer managed-by label")
	}
}

// TestMaterializeSkipsCCMSecretWhenDisabled verifies that with no CCM annotation
// (cloudControllerManager disabled) the aso-secret is still materialized but no
// cloud-config Secret is created — guarding the shared-namespace contract where a
// stray <release>-cloud-provider Secret must not appear unless CCM is on.
func TestMaterializeSkipsCCMSecretWhenDisabled(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: false,
	})
	reconcileCluster(t, materializer)

	// aso-secret is always materialized.
	getSecret(t, materializer, testClusterName+asoSecretSuffix)

	// The CCM cloud-config Secret must NOT exist when CCM is disabled.
	ccm := &corev1.Secret{}

	err := materializer.Get(context.Background(),
		types.NamespacedName{Namespace: testMatNamespace, Name: testCCMSecretName}, ccm)
	if err == nil {
		t.Fatal("CCM cloud-config Secret should not be created when CCM is disabled")
	}
}

// TestBuildAzureCloudConfigContract locks the generated CCM cloud-config
// byte-for-byte: the EXACT key set (no missing or extra keys), the derived values,
// and the value types. A dropped/renamed/retyped field here silently breaks the
// Azure CCM (LoadBalancer Services, node providerIDs) with no error surfaced — so
// the contract is asserted directly rather than via a field subset.
func TestBuildAzureCloudConfigContract(t *testing.T) {
	t.Parallel()

	creds := &azureCredentials{
		subscriptionID: testSubscription,
		tenantID:       testTenant,
		clientID:       testClientID,
		clientSecret:   testClientSecretPW,
		resourceGroup:  testRG,
		location:       testLocation,
		vnetName:       testVnetName,
		vnetRG:         testRG,
	}

	raw, err := buildAzureCloudConfig(testClusterName, creds)
	if err != nil {
		t.Fatalf("buildAzureCloudConfig returned error: %v", err)
	}

	var cfg map[string]any

	err = json.Unmarshal(raw, &cfg)
	if err != nil {
		t.Fatalf("cloud-config is not valid JSON: %v", err)
	}

	want := wantCloudConfig()

	if len(cfg) != len(want) {
		t.Fatalf("cloud-config has %d keys, want %d (keys present: %v)", len(cfg), len(want), keysOf(cfg))
	}

	for key, wantValue := range want {
		got, ok := cfg[key]
		if !ok {
			t.Errorf("cloud-config missing key %q", key)

			continue
		}

		if got != wantValue {
			t.Errorf("cloud-config[%q] = %v (%T), want %v (%T)", key, got, got, wantValue, wantValue)
		}
	}
}

// wantCloudConfig is the exhaustive expected cloud-config map. Numeric values are
// float64 because they are compared after a JSON round-trip.
func wantCloudConfig() map[string]any {
	return map[string]any{
		"tenantId":                     testTenant,
		"subscriptionId":               testSubscription,
		"resourceGroup":                testRG,
		"location":                     testLocation,
		"useManagedIdentityExtension":  false,
		"aadClientId":                  testClientID,
		"aadClientSecret":              testClientSecretPW,
		"loadBalancerSku":              ccmValueStandard,
		"vmType":                       ccmValueStandard,
		"useInstanceMetadata":          true,
		"securityGroupName":            testClusterName + "-node-nsg",
		"securityGroupResourceGroup":   testRG,
		"vnetName":                     testVnetName,
		"vnetResourceGroup":            testRG,
		"subnetName":                   testClusterName + "-node-subnet",
		"routeTableName":               testClusterName + "-node-routetable",
		"cloudProviderBackoff":         true,
		"cloudProviderBackoffRetries":  float64(ccmBackoffRetries),
		"cloudProviderRateLimit":       true,
		"cloudProviderRateLimitQPS":    ccmRateLimitQPS,
		"cloudProviderRateLimitBucket": float64(ccmRateLimitBucket),
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}

// TestClustersForIdentitySecretEnqueuesOnRotation verifies the watch-mapping that
// re-materializes credentials when the service principal password rotates: a
// change to the identity's clientSecret Secret must enqueue the owning Cluster.
func TestClustersForIdentitySecretEnqueuesOnRotation(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: true,
	})

	identitySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testIdentitySecret, Namespace: testMatNamespace},
	}

	requests := materializer.clustersForIdentitySecret(context.Background(), identitySecret)
	if len(requests) != 1 {
		t.Fatalf("expected 1 enqueued cluster on clientSecret rotation, got %d: %+v", len(requests), requests)
	}

	if requests[0].Name != testClusterName || requests[0].Namespace != testMatNamespace {
		t.Fatalf("enqueued wrong cluster: %+v", requests[0].NamespacedName)
	}
}

// TestClustersForIdentitySecretIgnoresManagedSecret guards the reconcile-loop
// prevention: the mapper must ignore Secrets it materialized itself (carrying the
// managed-by label), otherwise materializing a Secret would re-enqueue the cluster
// and spin forever.
func TestClustersForIdentitySecretIgnoresManagedSecret(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: true,
	})

	managedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName + asoSecretSuffix,
			Namespace: testMatNamespace,
			Labels:    map[string]string{materializerManagedByLabel: managedByLabelValue},
		},
	}

	requests := materializer.clustersForIdentitySecret(context.Background(), managedSecret)
	if requests != nil {
		t.Fatalf("materializer-managed Secret must not enqueue any cluster, got %+v", requests)
	}
}

// TestClustersForIdentitySecretIgnoresUnrelatedSecret verifies that a Secret no
// AzureClusterIdentity references enqueues nothing.
func TestClustersForIdentitySecretIgnoresUnrelatedSecret(t *testing.T) {
	t.Parallel()

	materializer := buildMaterializer(t, materializerFixture{
		identityType: infrav1.ServicePrincipal, withClientSecret: true, ccmEnabled: true,
	})

	unrelated := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-other-secret", Namespace: testMatNamespace},
	}

	requests := materializer.clustersForIdentitySecret(context.Background(), unrelated)
	if len(requests) != 0 {
		t.Fatalf("unrelated Secret must not enqueue any cluster, got %+v", requests)
	}
}
