package reconciler

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	capz_capi_controller "sigs.k8s.io/cluster-api-provider-azure/exp/controllers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
	return setupAzure(ctx, deps.Manager, deps.Options)
}

func setupAzure(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up AzureMachinePool controller")

	err := setupAzureMachinePoolWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachinePool controller: %w", err)
	}

	logger.Info("Setting up AzureMachine controller")

	err = setupAzureMachineWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachine controller: %w", err)
	}

	return nil
}

func setupAzureMachinePoolWithManager(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	err := (&capz_capi_controller.AzureMachinePoolReconciler{
		Client: manager.GetClient(),
		Scheme: manager.GetScheme(),
	}).SetupWithManager(ctx, manager,
		controllers.Options{Options: opt})
	if err != nil {
		return fmt.Errorf("failed to setup azure machine pool: %w", err)
	}

	return nil
}

func setupAzureMachineWithManager(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	err := (&controllers.AzureMachineReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		controllers.Options{Options: opt})
	if err != nil {
		return fmt.Errorf("failed to setup azure machine: %w", err)
	}

	return nil
}
