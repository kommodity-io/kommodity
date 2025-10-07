// Package controller provides the main controller manager for the Kommodity project.
package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	restclient "k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// MaxConcurrentReconciles is the maximum number of concurrent reconciles for controllers.
	MaxConcurrentReconciles = 10

	// RemoteConnectionGracePeriod is the grace period for remote connections.
	RemoteConnectionGracePeriod = 5 * time.Minute
)

// NewAggregatedControllerManager creates a new controller manager with all relevant providers.
//
//nolint:funlen // Too long due to many error checks and setup steps, no real complexity here
func NewAggregatedControllerManager(ctx context.Context,
	kommodityConfig *config.KommodityConfig,
	restConfig *restclient.Config,
	scheme *runtime.Scheme) (ctrl.Manager, error) {
	logger := zapr.NewLogger(logging.FromContext(ctx))
	ctrl.SetLogger(logger)

	logger.Info("Creating controller manager")

	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: scheme,
		Logger: logger,
		Cache: cache.Options{
			Scheme: scheme,
		},
		Client: client.Options{
			Scheme: scheme,
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
					&corev1.Pod{},
					&appsv1.Deployment{},
					&appsv1.DaemonSet{},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager: %w", err)
	}

	controllerOpts := controller.Options{
		MaxConcurrentReconciles: MaxConcurrentReconciles,
		LogConstructor: func(_ *reconcile.Request) logr.Logger {
			return logger
		},
	}

	clusterCache, err := setupClusterCacheWithManager(ctx, manager, controllerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterCache: %w", err)
	}

	// Core CAPI controllers

	logger.Info("Setting up CAPI controllers")

	err = setupCAPI(ctx, manager, clusterCache, controllerOpts, RemoteConnectionGracePeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to setup CAPI controllers: %w", err)
	}

	// Docker controllers for local development and testing only (KOMMODITY_DEVELOPMENT_MODE=true)
	if kommodityConfig.DevelopmentMode {
		logger.Info("Setting up Docker controllers")

		err = setupDocker(ctx, manager, clusterCache, controllerOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to setup Docker controllers: %w", err)
		}
	}

	// Talos controllers

	logger.Info("Setting up Talos controllers")

	err = setupTalos(ctx, manager, controllerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Talos controllers: %w", err)
	}

	// Azure controllers

	logger.Info("Setting up Azure controllers")

	err = setupAzure(ctx, manager, controllerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Azure controllers: %w", err)
	}

	// Scaleway controllers

	logger.Info("Setting up Scaleway controllers")

	err = setupScaleway(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Scaleway controllers: %w", err)
	}

	return manager, nil
}
