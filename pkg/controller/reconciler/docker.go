package reconciler

import (
	"context"
	"fmt"

	"sigs.k8s.io/cluster-api/controllers/clustercache"
	"sigs.k8s.io/cluster-api/test/infrastructure/container"
	docker_capi_controller "sigs.k8s.io/cluster-api/test/infrastructure/docker/controllers"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	ctrl "sigs.k8s.io/controller-runtime"
)

type dockerModule struct{}

// NewDockerModule creates a new module for Docker CAPI.
func NewDockerModule() Module {
	return &dockerModule{}
}

// Name returns the name of the module.
func (m *dockerModule) Name() config.Provider {
	return config.ProviderDocker
}

// Setup sets up the Docker CAPI controllers.
func (m *dockerModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupDocker(ctx, deps.Manager, deps.ClusterCache, deps.Options)
}

func setupDocker(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, opt controller.Options) error {
	logger := logging.FromContext(ctx)

	runtimeClient, err := container.NewDockerClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	logger.Info("Setting up DockerCluster controller")

	err = setupDockerClusterWithManager(ctx, manager, runtimeClient, opt)
	if err != nil {
		return fmt.Errorf("failed to setup DockerCluster controller: %w", err)
	}

	logger.Info("Setting up DockerMachine controller")

	err = setupDockerMachineWithManager(ctx, manager, clusterCache, runtimeClient, opt)
	if err != nil {
		return fmt.Errorf("failed to setup DockerMachine controller: %w", err)
	}

	return nil
}

func setupDockerClusterWithManager(ctx context.Context, manager ctrl.Manager,
	runtimeClient container.Runtime, opt controller.Options) error {
	err := (&docker_capi_controller.DockerClusterReconciler{
		Client:           manager.GetClient(),
		ContainerRuntime: runtimeClient,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup docker cluster: %w", err)
	}

	return nil
}

func setupDockerMachineWithManager(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache, runtimeClient container.Runtime, opt controller.Options) error {
	err := (&docker_capi_controller.DockerMachineReconciler{
		Client:           manager.GetClient(),
		ContainerRuntime: runtimeClient,
		ClusterCache:     clusterCache,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup docker machine: %w", err)
	}

	return nil
}
