package libkapi

import "net/http"

// HTTPHandlerFactory mounts additional routes onto the server's shared mux.
//
// Deprecated: use ServerFactory via WithServerFactory instead, which also
// exposes the gRPC server and the server's loopback client config through Ctx.
type HTTPHandlerFactory func(*http.ServeMux) error

// WithHTTPHandlerFactory mounts an additional set of routes onto the
// server's shared mux, alongside the built API server. Can be passed more
// than once; factories run in the order given.
//
// Deprecated: use WithServerFactory instead, which also exposes the gRPC
// server and the server's loopback client config through Ctx.
func WithHTTPHandlerFactory(factory HTTPHandlerFactory) Option {
	return WithServerFactory(func(c *Ctx) error {
		return factory(c.HTTPMux())
	})
}
