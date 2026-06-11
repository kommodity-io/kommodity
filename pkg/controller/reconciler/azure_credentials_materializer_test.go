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

	var cfg map[string]any

	err := json.Unmarshal(ccm.Data[ccmCloudConfigKey], &cfg)
	if err != nil {
		t.Fatalf("cloud-config is not valid JSON: %v", err)
	}

	wantCfg := map[string]any{
		"tenantId":                   testTenant,
		"subscriptionId":             testSubscription,
		"resourceGroup":              testRG,
		"location":                   testLocation,
		"aadClientId":                testClientID,
		"aadClientSecret":            testClientSecretPW,
		"securityGroupName":          testClusterName + "-node-nsg",
		"securityGroupResourceGroup": testRG,
		"vnetName":                   testVnetName,
		"vnetResourceGroup":          testRG,
		"subnetName":                 testClusterName + "-node-subnet",
		"routeTableName":             testClusterName + "-node-routetable",
		"loadBalancerSku":            "standard",
		"vmType":                     "standard",
	}
	for k, want := range wantCfg {
		if got := cfg[k]; got != want {
			t.Errorf("cloud-config[%s] = %v, want %v", k, got, want)
		}
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
