package controller

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	scaleway_capi_controller "github.com/scaleway/cluster-api-provider-scaleway/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"
)

func setupScaleway(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ScalewayCluster controller")

	err := setupScalewayClusterWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayCluster controller: %w", err)
	}

	logger.Info("Setting up ScalewayMachine controller")

	err = setupScalewayMachineWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayMachine controller: %w", err)
	}

	return nil
}

func setupScalewayMachineWithManager(_ context.Context, manager ctrl.Manager) error {
	err := scaleway_capi_controller.NewScalewayMachineReconciler(manager.GetClient()).SetupWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup scaleway machine: %w", err)
	}

	return nil
}

func setupScalewayClusterWithManager(ctx context.Context, manager ctrl.Manager) error {
	err := scaleway_capi_controller.NewScalewayClusterReconciler(manager.GetClient()).SetupWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup scaleway cluster: %w", err)
	}

	return nil
}
