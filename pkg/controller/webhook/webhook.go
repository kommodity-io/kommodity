// Package webhook provides the main controller manager for webhooks for the Kommodity project.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/provider"
	"go.uber.org/zap"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ErrUnservedWebhookPaths indicates that embedded webhook configurations reference
// admission paths with no handler registered on the in-process webhook server.
var ErrUnservedWebhookPaths = errors.New("webhook configurations reference paths with no registered handler")

// SetupWebhooks sets up all webhooks with the provided manager.
func SetupWebhooks(ctx context.Context,
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

	// CAPZ webhooks

	logger.Info("Setting up CAPZ webhooks")

	err = setupCAPZ(ctx, *manager)
	if err != nil {
		return fmt.Errorf("failed to setup CAPZ webhooks: %w", err)
	}

	// Every entry the embedded webhook configurations apply must have a handler on the
	// in-process webhook server: they are all installed with failurePolicy: Fail, so an
	// unregistered path makes the API server reject every matching admission request with
	// "failed calling webhook ... the server could not find the requested resource".
	// Entries that intentionally have no handler are dropped at apply time (see
	// provider.unservedWebhookPaths); anything still missing here is a wiring bug.
	err = verifyEmbeddedWebhooksAreServed(ctx, *manager)
	if err != nil {
		return fmt.Errorf("failed to verify embedded webhooks are served: %w", err)
	}

	return nil
}

// verifyEmbeddedWebhooksAreServed checks that every clientConfig path the embedded webhook
// configurations would install has a handler registered on the manager's webhook server,
// or is explicitly listed as unserved (and therefore dropped at apply time). A nil mux
// (nothing registered yet) means every path is unserved.
func verifyEmbeddedWebhooksAreServed(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	paths, err := provider.ServedWebhookPaths()
	if err != nil {
		return fmt.Errorf("failed to list embedded webhook paths: %w", err)
	}

	mux := manager.GetWebhookServer().WebhookMux()

	missing := make([]string, 0)

	for _, path := range paths {
		if mux == nil {
			missing = append(missing, path)

			continue
		}

		// Same probe controller-runtime's webhook builder uses in isAlreadyHandled: an
		// exact match returns the registered pattern, anything else falls through to
		// the mux's catch-all ("", notFoundHandler).
		handler, pattern := mux.Handler(&http.Request{URL: &url.URL{Path: path}})
		if handler != nil && pattern == path {
			continue
		}

		missing = append(missing, path)
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrUnservedWebhookPaths, strings.Join(missing, ", "))
	}

	logger.Info("All embedded webhook paths are served", zap.Int("count", len(paths)))

	return nil
}
