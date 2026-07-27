package libkapi

import (
	"fmt"
	"net/http"
)

// HTTPHandlerFactory mounts additional routes onto the server's shared mux.
// It is libkapi's own equivalent of combinedserver.HTTPMuxFactory, kept
// dependency-free so libkapi never imports pkg/combinedserver.
type HTTPHandlerFactory func(*http.ServeMux) error

// buildMux runs every factory against a fresh mux, then falls back every
// unmatched request to apiHandler - the built API server's own handler.
func buildMux(factories []HTTPHandlerFactory, apiHandler http.Handler) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	for i, factory := range factories {
		err := factory(mux)
		if err != nil {
			return nil, fmt.Errorf("failed to run HTTP handler factory %d: %w", i, err)
		}
	}

	mux.Handle("/", apiHandler)

	return mux, nil
}
