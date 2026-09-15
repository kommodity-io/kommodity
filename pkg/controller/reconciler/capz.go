package reconciler

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/controller/reconciler/azurearm"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	capz_reconciler "sigs.k8s.io/cluster-api-provider-azure/util/reconciler"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	azureClusterRecorderName = "azurecluster-controller"
	azureMachineRecorderName = "azuremachine-controller"
)

type azureModule struct{}

// NewAzureModule creates a new module for Azure CAPI.
func NewAzureModule() Module {
	return &azureModule{}
}

// Name returns the name of the module.
func (m *azureModule) Name() config.Provider {
	return config.ProviderAzure
}

// Setup sets up the Azure CAPI controllers.
func (m *azureModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupAzure(ctx, deps.Manager, deps.Options, deps.Config)
}

func setupAzure(
	ctx context.Context,
	manager ctrl.Manager,
	opt controller.Options,
	cfg *config.KommodityConfig,
) error {
	logger := logging.FromContext(ctx)

	credCache := azure.NewCredentialCache()
	timeouts := capz_reconciler.Timeouts{}

	logger.Info("Setting up AzureCluster controller")

	err := setupAzureClusterWithManager(ctx, manager, opt, credCache, timeouts)
	if err != nil {
		return fmt.Errorf("failed to setup AzureCluster controller: %w", err)
	}

	logger.Info("Setting up AzureMachine controller")

	err = setupAzureMachineWithManager(ctx, manager, opt, credCache, timeouts)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachine controller: %w", err)
	}

	// The AzureMachinePool controller is intentionally NOT registered. It is only
	// needed for MachinePool-based (e.g. AKS) topologies, which Kommodity does not
	// support — clusters use MachineDeployment -> AzureMachine. The upstream
	// AzureMachinePool reconciler additionally watches AzureManagedControlPlane,
	// whose CRD is excluded from the embedded provider set (see providers.yaml
	// deny_list). Registering it would block on a cache sync for that missing CRD
	// and prevent the controller manager (and its webhook server) from starting.

	var azureCfg *config.AzureConfig
	if cfg != nil {
		azureCfg = cfg.AzureConfig
	}

	err = azurearm.SetupReconcilers(ctx, manager, opt, azureCfg)
	if err != nil {
		return fmt.Errorf("failed to setup embedded Azure ARM reconciler: %w", err)
	}

	return nil
}

func setupAzureClusterWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt controller.Options,
	credCache azure.CredentialCache,
	timeouts capz_reconciler.Timeouts,
) error {
	recorder := manager.GetEventRecorderFor(azureClusterRecorderName)

	err := controllers.NewAzureClusterReconciler(
		manager.GetClient(),
		recorder,
		timeouts,
		"",
		credCache,
	).SetupWithManager(ctx, manager, controllers.Options{Options: opt})
	if err != nil {
		return fmt.Errorf("failed to setup azure cluster: %w", err)
	}

	return nil
}

func setupAzureMachineWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt controller.Options,
	credCache azure.CredentialCache,
	timeouts capz_reconciler.Timeouts,
) error {
	recorder := manager.GetEventRecorderFor(azureMachineRecorderName)

	err := controllers.NewAzureMachineReconciler(
		manager.GetClient(),
		recorder,
		timeouts,
		"",
		credCache,
	).SetupWithManager(ctx, manager, controllers.Options{Options: opt})
	if err != nil {
		return fmt.Errorf("failed to setup azure machine: %w", err)
	}

	return nil
}
