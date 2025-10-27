// Package attestation provides functionality for attestation services for establishing trust for the Talos machines.
package attestation

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/net"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for the attestation server.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		rateLimiter := net.NewRateLimiter()
		nounceStore := newNounceStore(cfg.AttestationConfig.NonceTTL)

		mux.HandleFunc("GET /nounce", getNounce(nounceStore, rateLimiter))
		mux.HandleFunc("POST /report", postReport(nounceStore, cfg))
		mux.HandleFunc("GET /report/{ip}/trust", getTrust(cfg))

		return nil
	}
}
