// Package attestation provides functionality for attestation services for establishing trust for the Talos machines.
package attestation

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for the attestation server.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		nounceStore := newNounceStore(cfg.AttestationConfig.NonceTTL)

		mux.HandleFunc("GET /nounce", getNounce(nounceStore))
		mux.HandleFunc("POST /report", postReport(nounceStore, cfg))
		mux.HandleFunc("GET /report/{ip}/trust", getTrust(cfg))

		return nil
	}
}
