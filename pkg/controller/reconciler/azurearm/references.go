package azurearm

import (
	"fmt"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
)

// buildResolvedDetails assembles the ConvertToARMResolvedDetails needed by a
// resource's generated ConvertToARM. Every resource reference found on the spec
// must resolve to an ARM ID.
//
// CAPZ expresses cross-resource links (subnet -> NSG/RouteTable/NatGateway) as
// genruntime.ResourceReference{ARMID: "<literal>"}, so resolution is simply
// keying each reference to its own ARM ID. The Kubernetes-style reference branch
// ({Group,Kind,Name}) is not used by CAPZ and is rejected here.
func buildResolvedDetails(obj genruntime.ARMMetaObject) (genruntime.ConvertToARMResolvedDetails, error) {
	references := findResourceReferences(obj.GetSpec())

	resolved := make(map[genruntime.ResourceReference]string, len(references))

	for reference := range references {
		if reference.ARMID == "" {
			return genruntime.ConvertToARMResolvedDetails{}, fmt.Errorf(
				"%w: %s (only ARM-ID references are supported)", ErrReferenceUnresolved, reference.String())
		}

		resolved[reference] = reference.ARMID
	}

	return genruntime.ConvertToARMResolvedDetails{
		Name:               obj.AzureName(),
		ResolvedReferences: genruntime.MakeResolved(resolved),
	}, nil
}
