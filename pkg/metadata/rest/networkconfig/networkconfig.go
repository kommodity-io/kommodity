// Package networkconfig provides handlers for network configuration metadata.
package networkconfig

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/config"
)

// GetNetworkConfig handles requests for network configuration metadata.
func GetNetworkConfig(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
	}
}
