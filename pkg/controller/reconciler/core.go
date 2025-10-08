package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	capi_controllers "sigs.k8s.io/cluster-api/controllers"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

//nolint:funlen // Too long due to many error checks and setup steps, no real complexity here
func setupCAPI(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options,
	remoteConnectionGracePeriod time.Duration) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ClusterClass controller")

	err := setupClusterClassWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterClass controller: %w", err)
	}

	logger.Info("Setting up Cluster controller")

	err = setupClusterWithManager(ctx, manager, clusterCache, opt, remoteConnectionGracePeriod)
	if err != nil {
		return fmt.Errorf("failed to setup cluster controller: %w", err)
	}

	logger.Info("Setting up Machine controller")

	err = setupMachineWithManager(ctx, manager, clusterCache, opt, remoteConnectionGracePeriod)
	if err != nil {
		return fmt.Errorf("failed to setup Machine controller: %w", err)
	}

	logger.Info("Setting up MachineSet controller")

	err = setupMachineSetWithManager(ctx, manager, clusterCache, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineSet controller: %w", err)
	}

	logger.Info("Setting up MachineDeployment controller")

	err = setupMachineDeploymentWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDeployment controller: %w", err)
	}

	logger.Info("Setting up ClusterResourceSet controller")

	err = setupClusterResourceSetWithManager(ctx, manager, clusterCache, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSet controller: %w", err)
	}

	logger.Info("Setting up ClusterResourceSetBinding controller")

	err = setupClusterResourceSetBindingWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSetBinding controller: %w", err)
	}

	logger.Info("Setting up MachineHealthCheck controller")

	err = setupMachineHealthCheckWithManager(ctx, manager, clusterCache, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineHealthCheck controller: %w", err)
	}

	return nil
}

func setupClusterWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options,
	remoteConnectionGracePeriod time.Duration) error {
	err := (&capi_controllers.ClusterReconciler{
		Client:                      manager.GetClient(),
		APIReader:                   manager.GetAPIReader(),
		ClusterCache:                clusterCache,
		RemoteConnectionGracePeriod: remoteConnectionGracePeriod,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup Cluster: %w", err)
	}

	return nil
}

func setupClusterClassWithManager(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	err := (&capi_controllers.ClusterClassReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterClass: %w", err)
	}

	return nil
}

func setupMachineWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options,
	remoteConnectionGracePeriod time.Duration) error {
	err := (&capi_controllers.MachineReconciler{
		Client:                      manager.GetClient(),
		APIReader:                   manager.GetAPIReader(),
		ClusterCache:                clusterCache,
		RemoteConditionsGracePeriod: remoteConnectionGracePeriod,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup Machine: %w", err)
	}

	return nil
}

func setupMachineDeploymentWithManager(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	err := (&capi_controllers.MachineDeploymentReconciler{
		Client:    manager.GetClient(),
		APIReader: manager.GetAPIReader(),
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDeployment: %w", err)
	}

	return nil
}

func setupMachineSetWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options) error {
	err := (&capi_controllers.MachineSetReconciler{
		Client:       manager.GetClient(),
		APIReader:    manager.GetAPIReader(),
		ClusterCache: clusterCache,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineSet: %w", err)
	}

	return nil
}

func setupMachineHealthCheckWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options) error {
	err := (&capi_controllers.MachineHealthCheckReconciler{
		Client:       manager.GetClient(),
		ClusterCache: clusterCache,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup MachineHealthCheck: %w", err)
	}

	return nil
}

func setupClusterResourceSetWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options) error {
	err := (&capi_controllers.ClusterResourceSetReconciler{
		Client:       manager.GetClient(),
		ClusterCache: clusterCache,
	}).SetupWithManager(ctx, manager, opt, manager.GetCache())
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSet: %w", err)
	}

	return nil
}

func setupClusterResourceSetBindingWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt controller.Options) error {
	err := (&capi_controllers.ClusterResourceSetBindingReconciler{
		Client: manager.GetClient(),
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSetBinding: %w", err)
	}

	return nil
}
