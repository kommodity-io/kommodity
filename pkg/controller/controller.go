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
func NewAggregatedControllerManager(ctx context.Context, config *restclient.Config) (ctrl.Manager, error) {
	logger := zapr.NewLogger(logging.FromContext(ctx))
	ctrl.SetLogger(logger)

	logger.Info("Creating controller manager")

	manager, err := ctrl.NewManager(config, ctrl.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager: %w", err)
	}

	logger.Info("Setting up talos bootstrap provider")

	err = setupBootstrapProviderWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup talos bootstrap provider: %w", err)
	}

	logger.Info("Setting up talos control plane provider")

	err = setupControlPlaneProviderWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup talos control plane provider: %w", err)
	}

	logger.Info("Setting up azure machine pool provider")

	err = setupAzureMachinePoolWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup azure machine pool provider: %w", err)
	}

	logger.Info("Setting up azure machine provider")

	err = setupAzureMachineWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup azure machine provider: %w", err)
	}

	logger.Info("Setting up scaleway machine provider")

	err = setupScalewayMachineWithManager(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to setup scaleway machine provider: %w", err)
	}

	logger.Info("Setting up scaleway cluster provider")

	err = setupScalewayClusterWithManager(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to setup scaleway cluster provider: %w", err)
	}

	return manager, nil
}
