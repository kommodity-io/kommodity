// Package metadata provides functionality for metadata services for the Talos machines.
package metadata

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	restnetworkconfig "github.com/kommodity-io/kommodity/pkg/metadata/rest/networkconfig"
	restuserdata "github.com/kommodity-io/kommodity/pkg/metadata/rest/userdata"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for the metadata server.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		mux.HandleFunc("GET /configs/user-data", restuserdata.GetUserData(cfg))
		mux.HandleFunc("GET /configs/network-config", restnetworkconfig.GetNetworkConfig(cfg))

		return nil
	}
}
