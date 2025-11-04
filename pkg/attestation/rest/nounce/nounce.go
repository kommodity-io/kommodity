// Package nounce provides the handler for the nounce endpoint.
package nounce

import (
	"encoding/json"
	"net/http"
	"time"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/net"
)

// NounceResponse represents the response structure for the nounce endpoint.
//
//nolint:revive // Struct name is appropriate for the context as its exposed via OpenAPI.
type NounceResponse struct {
	Nounce    string    `example:"884f2638c74645b859f87e76560748cc" json:"nounce"`
	ExpiresAt time.Time `format:"date-time"                         json:"expiresAt"`
}

// GetNounce godoc
// @Summary  Obtain an attestation nounce
// @Tags     Attestation
// @Success  200  {object}  NounceResponse
// @Failure  400  {object}  string   "If the request is invalid"
// @Failure  405  {object}  string   "If the method is not allowed"
// @Failure  429  {object}  string   "If the rate limit is exceeded"
// @Failure  500  {object}  string   "If there is a server error"
// @Router   /nounce [get]
//
// GetNounce handles the GET /nounce endpoint.
func GetNounce(nounceStore *restutils.NounceStore,
	rateLimiter *net.RateLimiter) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		//nolint:varnamelen // Variable name ip is appropriate for the context.
		ip, err := net.GetOriginalIPFromRequest(request)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)

			return
		}

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

		nounceResponse := NounceResponse{
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
