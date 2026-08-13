package reconciler

import (
	"context"
	"fmt"

	byot_controller "github.com/kommodity-io/cluster-api-provider-bringyourowntalos/pkg/controller"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
)

type byotModule struct{}

// NewByotModule creates a new module for the Bring Your Own Talos provider.
func NewByotModule() Module {
	return &byotModule{}
}

// Name returns the name of the module.
func (m *byotModule) Name() config.Provider {
	return config.ProviderByot
}

// Setup sets up the byot controllers.
func (m *byotModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupByot(ctx, deps.Manager, deps.Options)
}

func setupByot(ctx context.Context, manager ctrl.Manager, opt ctrlcontroller.Options) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ByotCluster controller")

	err := setupByotClusterWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ByotCluster controller: %w", err)
	}

	logger.Info("Setting up ByotMachine controller")

	err = setupByotMachineWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ByotMachine controller: %w", err)
	}

	return nil
}

func setupByotClusterWithManager(manager ctrl.Manager, opt ctrlcontroller.Options) error {
	err := byot_controller.NewByotClusterReconciler(manager.GetClient()).
		SetupWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ByotCluster reconciler: %w", err)
	}

	return nil
}

func setupByotMachineWithManager(manager ctrl.Manager, opt ctrlcontroller.Options) error {
	err := byot_controller.NewByotMachineReconciler(manager.GetClient()).
		SetupWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ByotMachine reconciler: %w", err)
	}

	return nil
}
