package reconciler

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	kubevirt_capi_controller "sigs.k8s.io/cluster-api-provider-kubevirt/controllers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

type kubevirtModule struct{}

// NewKubevirtModule creates a new module for Kubevirt CAPI.
func NewKubevirtModule() Module {
	return &kubevirtModule{}
}

// Name returns the name of the module.
func (m *kubevirtModule) Name() config.Provider {
	return config.ProviderKubevirt
}

// Setup sets up the Kubevirt CAPI controllers.
func (m *kubevirtModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupKubevirt(ctx, deps.Manager, deps.Options)
}

func setupKubevirt(ctx context.Context, manager ctrl.Manager, options controller.Options) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up KubevirtCluster controller")

	err := setupKubevirtClusterWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup KubevirtCluster controller: %w", err)
	}

	logger.Info("Setting up KubevirtMachine controller")

	err = setupKubevirtMachineWithManager(ctx, manager, options)
	if err != nil {
		return fmt.Errorf("failed to setup KubevirtMachine controller: %w", err)
	}

	return nil
}

func setupKubevirtClusterWithManager(ctx context.Context, manager ctrl.Manager) error {
	err := (&kubevirt_capi_controller.KubevirtClusterReconciler{
		Client: manager.GetClient(),
		Log:    zapr.NewLogger(logging.FromContext(ctx)),
	}).SetupWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup kubevirt cluster: %w", err)
	}

	return nil
}

func setupKubevirtMachineWithManager(ctx context.Context, manager ctrl.Manager, options controller.Options) error {
	err := (&kubevirt_capi_controller.KubevirtMachineReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager, options)
	if err != nil {
		return fmt.Errorf("failed to setup kubevirt machine: %w", err)
	}

	return nil
}
