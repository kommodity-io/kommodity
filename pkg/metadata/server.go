package metadata

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	restuserdata "github.com/kommodity-io/kommodity/pkg/metadata/rest/userdata"
)

// NewHTTPMuxFactory creates a new HTTP mux factory for the metadata server.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		mux.HandleFunc("GET /configs/user-data", restuserdata.GetUserData(cfg))

		return nil
	}
}
