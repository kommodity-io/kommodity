// Package reconciler provides the main controller manager for reconcilers for the Kommodity project.
package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	// RemoteConnectionGracePeriod is the grace period for remote connections.
	RemoteConnectionGracePeriod = 5 * time.Minute
)

// SetupReconcilers sets up all reconcilers with the provided manager.
func SetupReconcilers(ctx context.Context,
	kommodityConfig *config.KommodityConfig,
	manager *ctrl.Manager,
	clusterCache clustercache.ClusterCache,
	controllerOpts controller.Options) error {
	logger := logging.FromContext(ctx)
	// Core CAPI controllers

	logger.Info("Setting up CAPI controllers")

	err := setupCAPI(ctx, *manager, clusterCache, controllerOpts, RemoteConnectionGracePeriod)
	if err != nil {
		return fmt.Errorf("failed to setup CAPI controllers: %w", err)
	}

	// Docker controllers for local development and testing only (KOMMODITY_DEVELOPMENT_MODE=true)
	if kommodityConfig.DevelopmentMode {
		logger.Info("Setting up Docker controllers")

		err = setupDocker(ctx, *manager, clusterCache, controllerOpts)
		if err != nil {
			return fmt.Errorf("failed to setup Docker controllers: %w", err)
		}
	}

	// Talos controllers

	logger.Info("Setting up Talos controllers")

	err = setupTalos(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup Talos controllers: %w", err)
	}

	// Azure controllers

	logger.Info("Setting up Azure controllers")

	err = setupAzure(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup Azure controllers: %w", err)
	}

	// Scaleway controllers

	logger.Info("Setting up Scaleway controllers")

	err = setupScaleway(ctx, *manager)
	if err != nil {
		return fmt.Errorf("failed to setup Scaleway controllers: %w", err)
	}

	return nil
}
