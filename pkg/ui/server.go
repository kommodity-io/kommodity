// Package ui implements the Kommodity web user interface server.
package ui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
)

//go:embed public
var publicFS embed.FS

// NewHTTPMuxFactory creates a new HTTP mux factory for serving the Kommodity UI.
func NewHTTPMuxFactory(
	ctx context.Context,
	cfg *config.KommodityConfig,
) combinedserver.HTTPMuxFactory {
	logger := logging.FromContext(ctx)
	logger.Info("Initializing Kommodity UI server")

	return func(mux *http.ServeMux) error {
		// Serve embedded UI assets
		publicSubFS, err := fs.Sub(publicFS, "public")
		if err != nil {
			return fmt.Errorf("failed to create public filesystem: %w", err)
		}

		mux.Handle("GET /ui/public/", http.StripPrefix("/ui/public/", http.FileServer(http.FS(publicSubFS))))

		// UI router with HTMX templates
		router := NewRouter(cfg, logger)
		router.RegisterRoutes(mux)

		return nil
	}
}
