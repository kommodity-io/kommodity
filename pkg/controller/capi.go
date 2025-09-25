package controller

import (
	"context"
	"fmt"

	capi_controllers "sigs.k8s.io/cluster-api/controllers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func setupClusterWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.ClusterReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup Cluster: %w", err)
	}

	return nil
}

func setupClusterClassWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.ClusterClassReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup ClusterClass: %w", err)
	}

	return nil
}

func setupMachineWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.MachineReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup Machine: %w", err)
	}

	return nil
}

func setupMachineDeploymentWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.MachineDeploymentReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup MachineDeployment: %w", err)
	}

	return nil
}

func setupMachineSetWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.MachineSetReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup MachineSet: %w", err)
	}

	return nil
}

func setupMachineHealthCheckWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.MachineHealthCheckReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup MachineHealthCheck: %w", err)
	}

	return nil
}

func setupClusterResourceSetWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.ClusterResourceSetReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles},
		manager.GetCache())
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSet: %w", err)
	}

	return nil
}

func setupClusterResourceSetBindingWithManager(ctx context.Context, manager ctrl.Manager, maxConcurrentReconciles int) error {
	err := (&capi_controllers.ClusterResourceSetBindingReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager,
		 controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSetBinding: %w", err)
	}

	return nil
}
