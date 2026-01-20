// Package reconciler provides the main controller manager for reconcilers for the Kommodity project.
package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	// RemoteConnectionGracePeriod is the grace period for remote connections.
	RemoteConnectionGracePeriod = 5 * time.Minute
)

// SigningKeyDeps contains dependencies for the SigningKeyReconciler.
type SigningKeyDeps struct {
	CoreV1Client          corev1client.CoreV1Interface
	GetOrCreateSigningKey func(ctx context.Context, client corev1client.CoreV1Interface) (any, error)
}

// SetupReconcilers sets up all reconcilers with the provided manager.
func SetupReconcilers(ctx context.Context,
	cfg *config.KommodityConfig,
	manager *ctrl.Manager,
	clusterCache clustercache.ClusterCache,
	controllerOpts controller.Options,
	signingKeyDeps SigningKeyDeps) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up reconcilers",
		zap.Any("providers", cfg.InfrastructureProviders))

	providerFactories := NewReconcilerFactory()

	providers, err := providerFactories.Build(cfg)
	if err != nil {
		return fmt.Errorf("failed to build provider modules: %w", err)
	}

	for provider, providerFunc := range providers {
		logger.Info("Setting up reconciler for provider", zap.String("provider", string(provider)))

		err := providerFunc.Setup(ctx, SetupDeps{
			Manager:      *manager,
			ClusterCache: clusterCache,
			Options:      controllerOpts,
		})
		if err != nil {
			return fmt.Errorf("failed to setup reconciler for provider %s: %w", string(provider), err)
		}
	}

	err = setUpExtraReconcilers(ctx, cfg, manager, clusterCache, controllerOpts, signingKeyDeps)
	if err != nil {
		return fmt.Errorf("failed to setup extra reconcilers: %w", err)
	}

	return nil
}

func setUpExtraReconcilers(ctx context.Context,
	cfg *config.KommodityConfig,
	manager *ctrl.Manager,
	clusterCache clustercache.ClusterCache,
	controllerOpts controller.Options,
	signingKeyDeps SigningKeyDeps) error {
	err := (&CloudControllerManagerReconciler{
		Client: (*manager).GetClient(),
	}).SetupWithManager(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup CloudControllerManager reconciler: %w", err)
	}

	err = (&AutoscalerReconciler{
		Client: (*manager).GetClient(),
		cfg:    cfg,
	}).SetupWithManager(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup Autoscaler reconciler: %w", err)
	}

	err = (&ExtraSecretsManagerReconciler{
		Client: (*manager).GetClient(),
	}).SetupWithManager(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup ExtraSecretsManager reconciler: %w", err)
	}

	err = (&SigningKeyReconciler{
		Client:                (*manager).GetClient(),
		CoreV1Client:          signingKeyDeps.CoreV1Client,
		GetOrCreateSigningKey: signingKeyDeps.GetOrCreateSigningKey,
	}).SetupWithManager(ctx, *manager, controllerOpts)
	if err != nil {
		return fmt.Errorf("failed to setup SigningKey reconciler: %w", err)
	}

	return nil
}
