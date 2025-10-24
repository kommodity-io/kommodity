// Package attestation provides functionality for attestation services for establishing trust for the Talos machines.
package attestation

import (
	"context"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
)

func NewHTTPMuxFactory(ctx context.Context, cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		// Register your HTTP handlers here
		return nil
	}
}
