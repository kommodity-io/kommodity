// Package azurearm is an in-process reconciler that materializes Azure Service
// Operator (ASO) custom resources directly into Azure via the Azure Resource
// Manager (ARM) API. It is a drop-in replacement for the external ASO operator
// for the specific resource kinds CAPZ drives (ResourceGroup, VirtualNetwork,
// VirtualNetworksSubnet, NetworkSecurityGroup, NetworkSecurityGroupsSecurityRule,
// RouteTable, NatGateway).
//
// # Why this exists (why we built it instead of running ASO)
//
// Kommodity ships and deploys as a single binary (e.g. an Azure Container App)
// with no sidecars. Provisioning an Azure cluster requires something to turn the
// ASO CRs that CAPZ creates into real Azure resources — upstream, that "something"
// is the ASO operator, which is only distributed as a separate Deployment/sidecar.
// A deployed Kommodity instance therefore had no ASO at all and could not provision
// Azure clusters. Running ASO alongside us would also mean two binaries, a second
// version matrix (CAPZ ↔ ASO), and an extra Pod to deploy and secure.
//
// We could not simply import and embed ASO's controller, either: the part that
// actually talks to ARM — its generic ARM-by-ID client, ARM-ID construction,
// reference resolution, and the reconcile/poll loop — lives entirely under ASO's
// internal/ packages, which the Go toolchain forbids importing from another module.
//
// So we reuse everything ASO exposes publicly and reimplement only the engine that
// is locked away. Reused as-is from ASO's public surface: the generated CR types
// and their ConvertToARM (spec → ARM body) and PopulateFromARM (ARM → status)
// methods, plus pkg/genruntime and pkg/common/annotations. Hand-written here: the
// ARM client (armclient.go), ARM-ID construction (armid.go), reference resolution
// (references.go, reflecthelpers.go), credential loading (credentials.go), and the
// generic controller loop (reconciler.go). The Ready condition we publish
// (conditions.go) matches the exact reason/severity vocabulary the Cluster API
// Provider Azure (CAPZ) controllers read, so CAPZ cannot tell our reconciler apart
// from upstream ASO.
//
// The net effect: a single Kommodity binary provisions Azure end-to-end with no
// ASO sidecar.
//
// # How CAPZ and ASO split the work
//
// CAPZ does not provision all Azure resources the same way. The work is split:
//
//   - Network foundation — ResourceGroup, VirtualNetwork, VirtualNetworksSubnet,
//     NetworkSecurityGroup (+ SecurityRule), RouteTable, NatGateway — is delegated
//     to ASO. CAPZ only *creates the ASO custom resources*; it makes no ARM calls
//     for these itself. For this subset CAPZ is purely a translation layer, and
//     without an ASO reconciler the CRs just sit there — nothing reaches Azure.
//     This is the gap this package fills, and it is exactly the set of kinds in
//     registry.go.
//   - Compute and its immediate dependencies — virtual machines, OS disks, network
//     interfaces, load balancers, and public IPs — are provisioned by CAPZ's own
//     controllers calling the Azure SDK directly. These never become ASO CRs (the
//     apiserver does not even serve compute.azure.com / loadBalancers /
//     networkInterfaces / publicIPAddresses CRDs), so this package neither sees nor
//     manages them.
//
// The practical consequence, and the reason the embedded reconciler is mandatory
// rather than optional: CAPZ on its own cannot bring up a cluster. It would create
// the VMs/NICs/LB/PIPs, but the network foundation (the ASO CRs) would never
// reconcile, leaving the NICs with no subnet to attach to. Something has to turn
// those ASO CRs into Azure resources — upstream that is the ASO operator; here it
// is this package.
package azurearm

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// SetupReconcilers registers one generic ARM reconciler per managed Azure
// resource kind against the provided manager.
func SetupReconcilers(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
	azureCfg *config.AzureConfig,
) error {
	logger := logging.FromContext(ctx)

	resources := managedResources()

	logger.Info("Setting up embedded Azure ARM reconciler",
		zap.Int("managedResourceKinds", len(resources)))

	creds := newCredentialProvider(mgr.GetClient(), azureCfg.DefaultCredentialSecret)

	for _, resource := range resources {
		reconciler := &Reconciler{
			Client:              mgr.GetClient(),
			controllerName:      resource.controllerName,
			newObj:              resource.newObj,
			armIDFor:            resource.armIDFor,
			creds:               creds,
			deletionGracePeriod: azureCfg.ARMDeletionGracePeriod,
		}

		err := reconciler.SetupWithManager(ctx, mgr, opt)
		if err != nil {
			return fmt.Errorf("setting up %s reconciler: %w", resource.controllerName, err)
		}
	}

	return nil
}
