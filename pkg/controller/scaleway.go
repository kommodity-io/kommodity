package controller

import (
	"context"
	"fmt"

	scaleway_capi_controller "github.com/scaleway/cluster-api-provider-scaleway/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"
)

func setupScalewayMachineWithManager(ctx context.Context, manager ctrl.Manager) error {
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
