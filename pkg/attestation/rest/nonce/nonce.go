// Package nonce provides the handler for the nonce endpoint.
package nonce

import (
	"encoding/json"
	"net/http"
	"time"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/net"
)

// NonceResponse represents the response structure for the nonce endpoint.
//
//nolint:revive // Struct name is appropriate for the context as its exposed via OpenAPI.
type NonceResponse struct {
	Nonce     string    `example:"884f2638c74645b859f87e76560748cc" json:"nonce"`
	ExpiresAt time.Time `format:"date-time"                         json:"expiresAt"`
}

// GetNonce godoc
// @Summary  Obtain an attestation nonce
// @Tags     Attestation
// @Success  200  {object}  NonceResponse
// @Failure  400  {object}  string   "If the request is invalid"
// @Failure  405  {object}  string   "If the method is not allowed"
// @Failure  429  {object}  string   "If the rate limit is exceeded"
// @Failure  500  {object}  string   "If there is a server error"
// @Router   /nonce [get]
//
// GetNonce handles the GET /nonce endpoint.
func GetNonce(nonceStore *restutils.NonceStore,
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

		nonce, ttl, err := nonceStore.Generate(ip)
		if err != nil {
			http.Error(response, "Failed to generate nonce", http.StatusInternalServerError)

			return
		}

		nonceResponse := NonceResponse{
			Nonce:     nonce,
			ExpiresAt: ttl,
		}

		err = json.NewEncoder(response).Encode(nonceResponse)
		if err != nil {
			http.Error(response, "Failed to encode response", http.StatusInternalServerError)

			return
		}

		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
	}
}
