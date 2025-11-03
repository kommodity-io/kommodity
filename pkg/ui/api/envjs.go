package api

import (
	"encoding/json"
	"net/http"
	"os"
)

// EnvJSHandler serves a JavaScript file that sets environment variables on the client side.
func EnvJSHandler(allowlist []string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		response.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")

		cfg := map[string]string{}

		for _, k := range allowlist {
			if v, ok := os.LookupEnv(k); ok {
				cfg[k] = v
			}
		}

		b, _ := json.Marshal(cfg)
		_, _ = response.Write([]byte("window.__ENV__ = Object.freeze(" + string(b) + ");"))
	}
}
