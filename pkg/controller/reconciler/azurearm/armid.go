package azurearm

import (
	"context"
	"fmt"

	networkv1 "github.com/Azure/azure-service-operator/v2/api/network/v1api20201101"
	networkv20240301 "github.com/Azure/azure-service-operator/v2/api/network/v1api20240301"
	resourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// armIDFunc builds the fully-qualified ARM ID for a resource. ctx and kubeClient
// are available for owner-chain lookups via the Kubernetes API; simple root-level
// resources (e.g. ResourceGroup) may ignore them.
type armIDFunc func(
	ctx context.Context,
	kubeClient client.Client,
	obj genruntime.ARMMetaObject,
	subscriptionID string,
) (string, error)

// resourceGroupARMID builds the ARM ID for a ResourceGroup, which is rooted
// directly at the subscription. ctx and kubeClient are unused.
func resourceGroupARMID(
	_ context.Context,
	_ client.Client,
	obj genruntime.ARMMetaObject,
	subscriptionID string,
) (string, error) {
	if subscriptionID == "" {
		return "", fmt.Errorf("%w: empty subscription ID", ErrARMIDUnresolvable)
	}

	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, obj.AzureName()), nil
}

// rgScopedARMID builds the ARM ID for any resource whose direct owner is a
// ResourceGroup. The ARM path segment is derived from obj.GetType() (e.g.
// "Microsoft.Network/virtualNetworks"), so this single function serves VirtualNetwork,
// NetworkSecurityGroup, RouteTable, and NatGateway without type-specific code.
//
// The ResourceGroup CR must already be provisioned (resource-id annotation set);
// otherwise ErrARMIDUnresolvable is returned and the reconciler requeues.
func rgScopedARMID(
	ctx context.Context,
	kubeClient client.Client,
	obj genruntime.ARMMetaObject,
	_ string,
) (string, error) {
	ownerRef := obj.Owner()
	if ownerRef == nil {
		return "", fmt.Errorf(
			"%w: %s %s/%s has no owner",
			ErrARMIDUnresolvable, obj.GetType(), obj.GetNamespace(), obj.GetName(),
		)
	}

	resourceGroup := &resourcesv1.ResourceGroup{}
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: ownerRef.Name}

	err := kubeClient.Get(ctx, key, resourceGroup)
	if err != nil {
		return "", fmt.Errorf("%w: getting owner ResourceGroup %s: %w", ErrARMIDUnresolvable, key, err)
	}

	rgID, ok := genruntime.GetResourceID(resourceGroup)
	if !ok {
		return "", fmt.Errorf("%w: owner ResourceGroup %s not yet provisioned", ErrARMIDUnresolvable, key)
	}

	// obj.GetType() returns e.g. "Microsoft.Network/virtualNetworks"; the ARM ID
	// path appends it as the provider + type segment under the resource group.
	return fmt.Sprintf("%s/providers/%s/%s", rgID, obj.GetType(), obj.AzureName()), nil
}

// virtualNetworksSubnetARMID builds the ARM ID for a VirtualNetworksSubnet by
// looking up its owner VirtualNetwork. The VNet must already be provisioned
// (resource-id annotation set) so the ARM path can be derived; otherwise
// ErrARMIDUnresolvable is returned and the reconciler requeues until the VNet
// reaches Ready.
func virtualNetworksSubnetARMID(
	ctx context.Context,
	kubeClient client.Client,
	obj genruntime.ARMMetaObject,
	_ string,
) (string, error) {
	ownerRef := obj.Owner()
	if ownerRef == nil {
		return "", fmt.Errorf(
			"%w: VirtualNetworksSubnet %s/%s has no owner",
			ErrARMIDUnresolvable, obj.GetNamespace(), obj.GetName(),
		)
	}

	vnet := &networkv1.VirtualNetwork{}
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: ownerRef.Name}

	err := kubeClient.Get(ctx, key, vnet)
	if err != nil {
		return "", fmt.Errorf("%w: getting owner VirtualNetwork %s: %w", ErrARMIDUnresolvable, key, err)
	}

	vnetID, ok := genruntime.GetResourceID(vnet)
	if !ok {
		return "", fmt.Errorf("%w: owner VirtualNetwork %s not yet provisioned", ErrARMIDUnresolvable, key)
	}

	return fmt.Sprintf("%s/subnets/%s", vnetID, obj.AzureName()), nil
}

// networkSecurityGroupsSecurityRuleARMID builds the ARM ID for a
// NetworkSecurityGroupsSecurityRule by looking up its owner NetworkSecurityGroup.
// The NSG must already be provisioned (resource-id annotation set); otherwise
// ErrARMIDUnresolvable is returned and the reconciler requeues.
func networkSecurityGroupsSecurityRuleARMID(
	ctx context.Context,
	kubeClient client.Client,
	obj genruntime.ARMMetaObject,
	_ string,
) (string, error) {
	ownerRef := obj.Owner()
	if ownerRef == nil {
		return "", fmt.Errorf(
			"%w: NetworkSecurityGroupsSecurityRule %s/%s has no owner",
			ErrARMIDUnresolvable, obj.GetNamespace(), obj.GetName(),
		)
	}

	nsg := &networkv20240301.NetworkSecurityGroup{}
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: ownerRef.Name}

	err := kubeClient.Get(ctx, key, nsg)
	if err != nil {
		return "", fmt.Errorf("%w: getting owner NetworkSecurityGroup %s: %w", ErrARMIDUnresolvable, key, err)
	}

	nsgID, ok := genruntime.GetResourceID(nsg)
	if !ok {
		return "", fmt.Errorf("%w: owner NetworkSecurityGroup %s not yet provisioned", ErrARMIDUnresolvable, key)
	}

	return fmt.Sprintf("%s/securityRules/%s", nsgID, obj.AzureName()), nil
}
