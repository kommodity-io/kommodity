//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"context"
	"errors"
	"testing"

	networkv1 "github.com/Azure/azure-service-operator/v2/api/network/v1api20201101"
	networkv20220701 "github.com/Azure/azure-service-operator/v2/api/network/v1api20220701"
	networkv20240301 "github.com/Azure/azure-service-operator/v2/api/network/v1api20240301"
	resourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testSubscriptionID = "sub-123"
	testRGARMID        = "/subscriptions/sub-123/resourceGroups/my-rg"
	testVNetARMID      = testRGARMID + "/providers/Microsoft.Network/virtualNetworks/my-vnet"
	testNSGARMID       = testRGARMID + "/providers/Microsoft.Network/networkSecurityGroups/my-nsg"
)

func newResourceGroup(azureName string) *resourcesv1.ResourceGroup {
	return &resourcesv1.ResourceGroup{
		Spec: resourcesv1.ResourceGroup_Spec{
			AzureName: azureName,
		},
	}
}

// newTestScheme builds a scheme with all ASO network API versions used by the
// embedded reconciler.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()

	err := resourcesv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("adding resources scheme: %v", err)
	}

	err = networkv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("adding network v1api20201101 scheme: %v", err)
	}

	err = networkv20220701.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("adding network v1api20220701 scheme: %v", err)
	}

	err = networkv20240301.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("adding network v1api20240301 scheme: %v", err)
	}

	return scheme
}

// provisionedRG returns a ResourceGroup with the resource-id annotation set.
//
//nolint:unparam // test helper; name is intentionally parameterised for clarity
func provisionedRG(name string, namespace string) *resourcesv1.ResourceGroup {
	return &resourcesv1.ResourceGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				genruntime.ResourceIDAnnotation: testRGARMID,
			},
		},
		Spec: resourcesv1.ResourceGroup_Spec{AzureName: "my-rg"},
	}
}

// provisionedVNet returns a VirtualNetwork with the resource-id annotation set.
func provisionedVNet(name string, namespace string) *networkv1.VirtualNetwork {
	return &networkv1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				genruntime.ResourceIDAnnotation: testVNetARMID,
			},
		},
		Spec: networkv1.VirtualNetwork_Spec{AzureName: "my-vnet"},
	}
}

// provisionedNSG returns a NetworkSecurityGroup with the resource-id annotation set.
func provisionedNSG(name string, namespace string) *networkv20240301.NetworkSecurityGroup {
	return &networkv20240301.NetworkSecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				genruntime.ResourceIDAnnotation: testNSGARMID,
			},
		},
		Spec: networkv20240301.NetworkSecurityGroup_Spec{AzureName: "my-nsg"},
	}
}

// --- ResourceGroup ---

func TestResourceGroupARMID(t *testing.T) {
	t.Parallel()

	resourceGroup := newResourceGroup("my-rg")

	got, err := resourceGroupARMID(context.Background(), nil, resourceGroup, testSubscriptionID)
	if err != nil {
		t.Fatalf("resourceGroupARMID returned error: %v", err)
	}

	want := "/subscriptions/sub-123/resourceGroups/my-rg"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestResourceGroupARMIDEmptySubscription(t *testing.T) {
	t.Parallel()

	resourceGroup := newResourceGroup("my-rg")

	_, err := resourceGroupARMID(context.Background(), nil, resourceGroup, "")
	if !errors.Is(err, ErrARMIDUnresolvable) {
		t.Fatalf("expected ErrARMIDUnresolvable, got %v", err)
	}
}

// --- RG-scoped resources (rgScopedARMID covers VNet, NSG, RouteTable, NatGateway) ---

func TestRGScopedARMIDVirtualNetwork(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerRG := provisionedRG("owner-rg", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerRG).
		Build()

	vnet := &networkv1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "my-vnet", Namespace: testNamespace},
		Spec: networkv1.VirtualNetwork_Spec{
			AzureName: "my-vnet",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-rg"},
		},
	}

	got, err := rgScopedARMID(context.Background(), kubeClient, vnet, testSubscriptionID)
	if err != nil {
		t.Fatalf("rgScopedARMID returned error: %v", err)
	}

	want := testRGARMID + "/providers/Microsoft.Network/virtualNetworks/my-vnet"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestRGScopedARMIDNetworkSecurityGroup(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerRG := provisionedRG("owner-rg", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerRG).
		Build()

	nsg := &networkv20240301.NetworkSecurityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "my-nsg", Namespace: testNamespace},
		Spec: networkv20240301.NetworkSecurityGroup_Spec{
			AzureName: "my-nsg",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-rg"},
		},
	}

	got, err := rgScopedARMID(context.Background(), kubeClient, nsg, testSubscriptionID)
	if err != nil {
		t.Fatalf("rgScopedARMID returned error: %v", err)
	}

	want := testRGARMID + "/providers/Microsoft.Network/networkSecurityGroups/my-nsg"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestRGScopedARMIDRouteTable(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerRG := provisionedRG("owner-rg", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerRG).
		Build()

	routeTable := &networkv20240301.RouteTable{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rt", Namespace: testNamespace},
		Spec: networkv20240301.RouteTable_Spec{
			AzureName: "my-rt",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-rg"},
		},
	}

	got, err := rgScopedARMID(context.Background(), kubeClient, routeTable, testSubscriptionID)
	if err != nil {
		t.Fatalf("rgScopedARMID returned error: %v", err)
	}

	want := testRGARMID + "/providers/Microsoft.Network/routeTables/my-rt"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestRGScopedARMIDNatGateway(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerRG := provisionedRG("owner-rg", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerRG).
		Build()

	natGateway := &networkv20220701.NatGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "my-nat", Namespace: testNamespace},
		Spec: networkv20220701.NatGateway_Spec{
			AzureName: "my-nat",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-rg"},
		},
	}

	got, err := rgScopedARMID(context.Background(), kubeClient, natGateway, testSubscriptionID)
	if err != nil {
		t.Fatalf("rgScopedARMID returned error: %v", err)
	}

	want := testRGARMID + "/providers/Microsoft.Network/natGateways/my-nat"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestRGScopedARMIDOwnerNotProvisioned(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)

	// RG exists but has no resource-id annotation.
	unprovisionedRG := &resourcesv1.ResourceGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "owner-rg", Namespace: testNamespace},
		Spec:       resourcesv1.ResourceGroup_Spec{AzureName: "my-rg"},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(unprovisionedRG).
		Build()

	vnet := &networkv1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "my-vnet", Namespace: testNamespace},
		Spec: networkv1.VirtualNetwork_Spec{
			AzureName: "my-vnet",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-rg"},
		},
	}

	_, err := rgScopedARMID(context.Background(), kubeClient, vnet, testSubscriptionID)
	if !errors.Is(err, ErrARMIDUnresolvable) {
		t.Fatalf("expected ErrARMIDUnresolvable, got %v", err)
	}
}

func TestRGScopedARMIDOwnerMissing(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	vnet := &networkv1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "my-vnet", Namespace: testNamespace},
		Spec: networkv1.VirtualNetwork_Spec{
			AzureName: "my-vnet",
			Owner:     &genruntime.KnownResourceReference{Name: "missing-rg"},
		},
	}

	_, err := rgScopedARMID(context.Background(), kubeClient, vnet, testSubscriptionID)
	if !errors.Is(err, ErrARMIDUnresolvable) {
		t.Fatalf("expected ErrARMIDUnresolvable, got %v", err)
	}
}

// --- VirtualNetworksSubnet ---

func TestVirtualNetworksSubnetARMID(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerVNet := provisionedVNet("owner-vnet", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerVNet).
		Build()

	subnet := &networkv1.VirtualNetworksSubnet{
		ObjectMeta: metav1.ObjectMeta{Name: "my-subnet", Namespace: testNamespace},
		Spec: networkv1.VirtualNetworksSubnet_Spec{
			AzureName: "my-subnet",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-vnet"},
		},
	}

	got, err := virtualNetworksSubnetARMID(context.Background(), kubeClient, subnet, testSubscriptionID)
	if err != nil {
		t.Fatalf("virtualNetworksSubnetARMID returned error: %v", err)
	}

	want := testVNetARMID + "/subnets/my-subnet"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestVirtualNetworksSubnetARMIDOwnerNotProvisioned(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)

	unprovisionedVNet := &networkv1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "owner-vnet", Namespace: testNamespace},
		Spec:       networkv1.VirtualNetwork_Spec{AzureName: "my-vnet"},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(unprovisionedVNet).
		Build()

	subnet := &networkv1.VirtualNetworksSubnet{
		ObjectMeta: metav1.ObjectMeta{Name: "my-subnet", Namespace: testNamespace},
		Spec: networkv1.VirtualNetworksSubnet_Spec{
			AzureName: "my-subnet",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-vnet"},
		},
	}

	_, err := virtualNetworksSubnetARMID(context.Background(), kubeClient, subnet, testSubscriptionID)
	if !errors.Is(err, ErrARMIDUnresolvable) {
		t.Fatalf("expected ErrARMIDUnresolvable, got %v", err)
	}
}

// --- NetworkSecurityGroupsSecurityRule ---

func TestNetworkSecurityGroupsSecurityRuleARMID(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ownerNSG := provisionedNSG("owner-nsg", testNamespace)

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ownerNSG).
		Build()

	rule := &networkv20240301.NetworkSecurityGroupsSecurityRule{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rule", Namespace: testNamespace},
		Spec: networkv20240301.NetworkSecurityGroupsSecurityRule_Spec{
			AzureName: "my-rule",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-nsg"},
		},
	}

	got, err := networkSecurityGroupsSecurityRuleARMID(context.Background(), kubeClient, rule, testSubscriptionID)
	if err != nil {
		t.Fatalf("networkSecurityGroupsSecurityRuleARMID returned error: %v", err)
	}

	want := testNSGARMID + "/securityRules/my-rule"
	if got != want {
		t.Fatalf("armID = %q, want %q", got, want)
	}
}

func TestNetworkSecurityGroupsSecurityRuleARMIDOwnerNotProvisioned(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)

	unprovisionedNSG := &networkv20240301.NetworkSecurityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "owner-nsg", Namespace: testNamespace},
		Spec:       networkv20240301.NetworkSecurityGroup_Spec{AzureName: "my-nsg"},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(unprovisionedNSG).
		Build()

	rule := &networkv20240301.NetworkSecurityGroupsSecurityRule{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rule", Namespace: testNamespace},
		Spec: networkv20240301.NetworkSecurityGroupsSecurityRule_Spec{
			AzureName: "my-rule",
			Owner:     &genruntime.KnownResourceReference{Name: "owner-nsg"},
		},
	}

	_, err := networkSecurityGroupsSecurityRuleARMID(context.Background(), kubeClient, rule, testSubscriptionID)
	if !errors.Is(err, ErrARMIDUnresolvable) {
		t.Fatalf("expected ErrARMIDUnresolvable, got %v", err)
	}
}
