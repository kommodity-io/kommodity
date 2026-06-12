package azurearm

import (
	networkv1 "github.com/Azure/azure-service-operator/v2/api/network/v1api20201101"
	networkv20220701 "github.com/Azure/azure-service-operator/v2/api/network/v1api20220701"
	networkv20240301 "github.com/Azure/azure-service-operator/v2/api/network/v1api20240301"
	resourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
)

// managedResource describes a single Azure resource kind the embedded ARM
// reconciler owns. Each entry is registered as its own controller (one generic
// Reconciler instance per kind) by SetupReconcilers.
type managedResource struct {
	// controllerName is the unique controller-runtime controller name.
	controllerName string
	// newObj constructs an empty typed object for this kind.
	newObj func() genruntime.ARMMetaObject
	// armIDFor builds the fully-qualified ARM ID for an object of this kind.
	armIDFor armIDFunc
}

// managedResources returns the table of Azure resource kinds the embedded ARM
// reconciler owns, covering all kinds that CAPZ and the kommodity-cluster Helm
// chart emit for a private Azure cluster.
func managedResources() []managedResource {
	return []managedResource{
		// Subscription-scoped root
		{
			controllerName: "azurearm-resourcegroup",
			newObj:         func() genruntime.ARMMetaObject { return &resourcesv1.ResourceGroup{} },
			armIDFor:       resourceGroupARMID,
		},

		// RG-scoped: CAPZ-created (network.azure.com/v1api20201101)
		{
			controllerName: "azurearm-virtualnetwork",
			newObj:         func() genruntime.ARMMetaObject { return &networkv1.VirtualNetwork{} },
			armIDFor:       rgScopedARMID,
		},

		// RG-scoped: CAPZ-created (network.azure.com/v1api20220701)
		{
			controllerName: "azurearm-natgateway",
			newObj:         func() genruntime.ARMMetaObject { return &networkv20220701.NatGateway{} },
			armIDFor:       rgScopedARMID,
		},

		// RG-scoped: chart-emitted (network.azure.com/v1api20240301)
		{
			controllerName: "azurearm-networksecuritygroup",
			newObj:         func() genruntime.ARMMetaObject { return &networkv20240301.NetworkSecurityGroup{} },
			armIDFor:       rgScopedARMID,
		},
		{
			controllerName: "azurearm-routetable",
			newObj:         func() genruntime.ARMMetaObject { return &networkv20240301.RouteTable{} },
			armIDFor:       rgScopedARMID,
		},

		// VNet-scoped child (network.azure.com/v1api20201101)
		{
			controllerName: "azurearm-virtualnetworkssubnet",
			newObj:         func() genruntime.ARMMetaObject { return &networkv1.VirtualNetworksSubnet{} },
			armIDFor:       virtualNetworksSubnetARMID,
		},

		// NSG-scoped child (network.azure.com/v1api20240301)
		{
			controllerName: "azurearm-networksecuritygroupssecurityrule",
			newObj: func() genruntime.ARMMetaObject {
				return &networkv20240301.NetworkSecurityGroupsSecurityRule{}
			},
			armIDFor: networkSecurityGroupsSecurityRuleARMID,
		},
	}
}
