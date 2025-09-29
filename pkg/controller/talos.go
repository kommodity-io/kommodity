package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	bootstrap_controller "github.com/siderolabs/cluster-api-bootstrap-provider-talos/controllers"
	control_plane_controller "github.com/siderolabs/cluster-api-control-plane-provider-talos/controllers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func setupTalos(ctx context.Context, manager ctrl.Manager,
	maxConcurrentReconciles int) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up TalosConfig controller")

	err := setupTalosConfigWithManager(ctx, manager, maxConcurrentReconciles)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig controller: %w", err)
	}

	logger.Info("Setting up TalosControlPlane controller")

	err = setupTalosControlPlaneWithManager(ctx, manager, maxConcurrentReconciles)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	return nil
}

func setupTalosConfigWithManager(ctx context.Context, manager ctrl.Manager,
	maxConcurrentReconciles int) error {
	err := (&bootstrap_controller.TalosConfigReconciler{
		Client: manager.GetClient(),
		Log:    zapr.NewLogger(logging.FromContext(ctx)),
		Scheme: manager.GetScheme(),
	}).SetupWithManager(ctx, manager, controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig controller: %w", err)
	}

	return nil
}

func setupTalosControlPlaneWithManager(ctx context.Context, manager ctrl.Manager,
	maxConcurrentReconciles int) error {
	err := (&control_plane_controller.TalosControlPlaneReconciler{
		Client: manager.GetClient(),
		Log:    zapr.NewLogger(logging.FromContext(ctx)),
		Scheme: manager.GetScheme(),
	}).SetupWithManager(manager, controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles})
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	return nil
}
