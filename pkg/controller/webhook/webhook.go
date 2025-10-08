// Package webhook provides the main controller manager for webhooks for the Kommodity project.
package webhook

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhooks sets up all webhooks with the provided manager.
func SetupWebhooks(ctx context.Context,
	kommodityConfig *config.KommodityConfig,
	manager *ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up Talos webhooks")

	err := setupTalos(ctx, *manager)
	if err != nil {
		return fmt.Errorf("failed to setup Talos webhooks: %w", err)
	}

	return nil
}
