// Package attestation provides functionality for attestation services for establishing trust for the Talos machines.
package attestation

import (
	"net/http"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	restnounce "github.com/kommodity-io/kommodity/pkg/attestation/rest/nounce"
	restreport "github.com/kommodity-io/kommodity/pkg/attestation/rest/report"
	resttrust "github.com/kommodity-io/kommodity/pkg/attestation/rest/trust"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/net"
)

const (
	// AttestationNounceEndpoint is the endpoint for obtaining a nonce.
	AttestationNounceEndpoint = "/nounce"

	// AttestationReportEndpoint is the endpoint for submitting an attestation report.
	AttestationReportEndpoint = "/report"

	// AttestationTrustEndpoint is the endpoint for checking trust status based on an attestation report.
	AttestationTrustEndpoint = "/report/{ip}/trust"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for the attestation server.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		rateLimiter := net.NewRateLimiter()
		nounceStore := restutils.NewNounceStore(cfg.AttestationConfig.NonceTTL)

		mux.HandleFunc("GET "+AttestationNounceEndpoint, restnounce.GetNounce(nounceStore, rateLimiter))
		mux.HandleFunc("POST "+AttestationReportEndpoint, restreport.PostReport(nounceStore, cfg))
		mux.HandleFunc("GET "+AttestationTrustEndpoint, resttrust.GetTrust(cfg))

		return nil
	}
}
