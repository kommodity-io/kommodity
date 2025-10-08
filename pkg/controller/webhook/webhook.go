// Package webhook provides the main controller manager for webhooks for the Kommodity project.
package webhook

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhooks sets up all webhooks with the provided manager.
func SetupWebhooks(ctx context.Context,
	kommodityConfig *config.KommodityConfig,
	manager *ctrl.Manager,
	clusterCache clustercache.ClusterCache) error {
	logger := logging.FromContext(ctx)

	// CAPI webhooks

	logger.Info("Setting up CAPI webhooks")

	err := setupCAPI(ctx, *manager, clusterCache)
	if err != nil {
		return fmt.Errorf("failed to setup CAPI webhooks: %w", err)
	}

	// Talos webhooks

	logger.Info("Setting up Talos webhooks")

	err = setupTalos(ctx, *manager)
	if err != nil {
		return fmt.Errorf("failed to setup Talos webhooks: %w", err)
	}

	return nil
}
