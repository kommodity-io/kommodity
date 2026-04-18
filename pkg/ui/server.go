// Package ui implements the Kommodity web user interface server.
package ui

import (
	"context"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for serving the Kommodity UI.
func NewHTTPMuxFactory(
	ctx context.Context,
	cfg *config.KommodityConfig,
) combinedserver.HTTPMuxFactory {
	logger := logging.FromContext(ctx)
	logger.Info("Initializing Kommodity UI server")

	return func(mux *http.ServeMux) error {
		// Serve static files from public directory
		publicFS := http.Dir("./public")
		mux.Handle("GET /public/", http.StripPrefix("/public/", http.FileServer(publicFS)))

		// UI router with HTMX templates
		router := NewRouter(cfg, logger)
		router.RegisterRoutes(mux)

		return nil
	}
}
