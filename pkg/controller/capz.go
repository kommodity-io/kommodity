package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	capz_capi_controller "sigs.k8s.io/cluster-api-provider-azure/exp/controllers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

//nolint:unused //To be used later
func setupAzureMachinePoolWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capz_capi_controller.AzureMachinePoolReconciler{
		Client: manager.GetClient(),
		Scheme: manager.GetScheme(),
	}).SetupWithManager(ctx, manager,
		controllers.Options{Options: controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}})
	if err != nil {
		return fmt.Errorf("failed to setup azure machine pool: %w", err)
	}

	return nil
}
