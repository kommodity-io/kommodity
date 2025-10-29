// Package userdata provides handlers for user data metadata.
package userdata

import (
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/config"
)

// GetUserData handles requests for user data metadata.
func GetUserData(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
	}
}
