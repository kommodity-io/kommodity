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
//
//nolint:ireturn
func NewAggregatedControllerManager(ctx context.Context, config *restclient.Config) (ctrl.Manager, error) {
	ctrl.SetLogger(zapr.NewLogger(logging.FromContext(ctx)))

	manager, err := ctrl.NewManager(config, ctrl.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager: %w", err)
	}

	err = setupBootstrapProviderWithManager(ctx, manager, MaxConcurrentReconciles)
	if err != nil {
		return nil, fmt.Errorf("failed to setup talos bootstrap provider: %w", err)
	}

	// err = setupControlPlaneProviderWithManager(ctx, manager, MaxConcurrentReconciles)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to setup talos control plane provider: %w", err)
	// }

	// err = setupAzureMachinePoolWithManager(ctx, manager, MaxConcurrentReconciles)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to setup azure machine pool: %w", err)
	// }

	return manager, nil
}
