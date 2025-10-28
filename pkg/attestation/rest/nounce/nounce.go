// Package nounce provides the handler for the nounce endpoint.
package nounce

import (
	"encoding/json"
	"net/http"
	"time"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/net"
)

type nounceResponse struct {
	Nounce    string    `json:"nounce"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// GetNounce handles the GET /nounce endpoint.
func GetNounce(nounceStore *restutils.NounceStore,
	rateLimiter *net.RateLimiter) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		//nolint:varnamelen // Variable name ip is appropriate for the context.
		ip := request.RemoteAddr

		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if !rateLimiter.GetClientLimiter(ip).Allow() {
			http.Error(response, "Rate limit exceeded", http.StatusTooManyRequests)

			return
		}

		nounce, ttl, err := nounceStore.Generate(ip)
		if err != nil {
			http.Error(response, "Failed to generate nounce", http.StatusInternalServerError)

			return
		}

		nounceResponse := nounceResponse{
			Nounce:    nounce,
			ExpiresAt: ttl,
		}

		err = json.NewEncoder(response).Encode(nounceResponse)
		if err != nil {
			http.Error(response, "Failed to encode response", http.StatusInternalServerError)

			return
		}

		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
	}
}
