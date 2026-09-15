package libkapi

import (
	"net/http"
	"strings"

	"google.golang.org/grpc"
)

// grpcContentTypePrefix identifies gRPC requests so muxHandler can route
// them to the gRPC server instead of the HTTP mux. Matches
// pkg/combinedserver's own routing rule.
const grpcContentTypePrefix = "application/grpc"

// GRPCServerFactory registers services on the server's embedded gRPC
// server. It is libkapi's own equivalent of
// combinedserver.GRPCServerFactory, kept dependency-free so libkapi never
// imports pkg/combinedserver.
//
// Deprecated: use ServerFactory via WithServerFactory instead, which also
// exposes the HTTP mux and the server's loopback client config through Ctx.
type GRPCServerFactory func(*grpc.Server) error

// WithGRPCServerFactory adds a gRPC server alongside the built API server,
// multiplexed onto the same listener address and port: requests whose
// Content-Type starts with "application/grpc" are routed to the gRPC
// server, everything else falls through to the HTTP mux (built API server
// plus any WithHTTPHandlerFactory routes) as before. Can be passed more
// than once; factories run in the order given, all against the same
// *grpc.Server. Reflection is registered automatically.
//
// Registering at least one factory switches the HTTP listener to serve h2c
// (HTTP/2 over plaintext) so gRPC's own HTTP/2 requirement is met without
// WithTLS (not yet implemented). Servers that never call this option are
// unaffected: the listener stays on plain HTTP/1.1. The h2c switch itself is
// applied in server.go's newHTTPServer via http.Server.Protocols, not here,
// because unencrypted HTTP/2 is a listener-level concern (the *http.Server),
// not a handler-level one.
//
// Deprecated: use WithServerFactory instead.
func WithGRPCServerFactory(factory GRPCServerFactory) Option {
	return WithServerFactory(func(c *Ctx) error {
		return factory(c.GRPCServer())
	})
}

// muxHandler routes a request to grpcServer when its Content-Type
// identifies it as gRPC, otherwise to httpHandler. If grpcServer is nil (no
// ServerFactory ever called Ctx.GRPCServer), every request goes to
// httpHandler.
func muxHandler(grpcServer *grpc.Server, httpHandler http.Handler) http.Handler {
	if grpcServer == nil {
		return httpHandler
	}

	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.Header.Get("Content-Type"), grpcContentTypePrefix) {
			grpcServer.ServeHTTP(resp, req)

			return
		}

		httpHandler.ServeHTTP(resp, req)
	})
}
