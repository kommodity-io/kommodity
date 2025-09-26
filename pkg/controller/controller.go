// Package controller provides the main controller manager for the Kommodity project.
package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	restclient "k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// MaxConcurrentReconciles is the maximum number of concurrent reconciles for controllers.
	MaxConcurrentReconciles = 10
)

// NewAggregatedControllerManager creates a new controller manager with all relevant providers.
//nolint:cyclop, funlen // Too long or too complex due to many error checks and setup steps, no real complexity here
func NewAggregatedControllerManager(ctx context.Context, config *restclient.Config) (ctrl.Manager, error) {
	logger := zapr.NewLogger(logging.FromContext(ctx))
	ctrl.SetLogger(logger)

	logger.Info("Creating controller manager")

	manager, err := ctrl.NewManager(config, ctrl.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager: %w", err)
	}

	// Core CAPI controllers

	logger.Info("Setting up Cluster controller")

	err = setupClusterWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup cluster controller: %w", err)
	}

	logger.Info("Setting up ClusterClass controller")

	err = setupClusterClassWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterClass controller: %w", err)
	}

	logger.Info("Setting up Machine controller")

	err = setupMachineWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Machine controller: %w", err)
	}

	logger.Info("Setting up MachineDeployment controller")

	err = setupMachineDeploymentWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup MachineDeployment controller: %w", err)
	}

	logger.Info("Setting up MachineSet controller")

	err = setupMachineSetWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup MachineSet controller: %w", err)
	}

	logger.Info("Setting up MachineHealthCheck controller")

	err = setupMachineHealthCheckWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup MachineHealthCheck controller: %w", err)
	}

	logger.Info("Setting up ClusterResourceSet controller")

	err = setupClusterResourceSetWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterResourceSet controller: %w", err)
	}

	logger.Info("Setting up ClusterResourceSetBinding controller")

	err = setupClusterResourceSetBindingWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterResourceSetBinding controller: %w", err)
	}

	// Talos controllers

	logger.Info("Setting up TalosConfig controller")

	err = setupTalosConfigWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TalosConfig controller: %w", err)
	}

	logger.Info("Setting up TalosControlPlane controller")

	err = setupTalosControlPlaneWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	// Azure controllers

	logger.Info("Setting up AzureMachinePool controller")

	err = setupAzureMachinePoolWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup AzureMachinePool controller: %w", err)
	}

	logger.Info("Setting up AzureMachine controller")

	err = setupAzureMachineWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup AzureMachine controller: %w", err)
	}

	// Scaleway controllers

	logger.Info("Setting up ScalewayMachine controller")

	err = setupScalewayMachineWithManager(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ScalewayMachine controller: %w", err)
	}

	logger.Info("Setting up ScalewayCluster controller")

	err = setupScalewayClusterWithManager(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ScalewayCluster controller: %w", err)
	}

	return manager, nil
}
