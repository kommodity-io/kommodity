package libkapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// grpcContentTypePrefix identifies gRPC requests so muxHandler can route
// them to the gRPC server instead of the HTTP mux. Matches
// pkg/combinedserver's own routing rule.
const grpcContentTypePrefix = "application/grpc"

// GRPCServerFactory registers services on the server's embedded gRPC
// server. It is libkapi's own equivalent of
// combinedserver.GRPCServerFactory, kept dependency-free so libkapi never
// imports pkg/combinedserver.
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
// applied in server.go via http.Server.Protocols, not here, because
// unencrypted HTTP/2 is a listener-level concern (the *http.Server), not a
// handler-level one.
func WithGRPCServerFactory(factory GRPCServerFactory) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.grpcFactories = append(cfg.grpcFactories, factory)

		return nil
	}
}

// buildHandler builds the gRPC server (if any factories were registered)
// and returns it alongside a handler that routes gRPC requests to the
// gRPC server and everything else to mux. The handler is returned
// unwrapped; unencrypted HTTP/2 (h2c) support is configured on the
// *http.Server in server.go when grpcServer is non-nil, via the Protocols
// field (replaces the deprecated golang.org/x/net/http2/h2c handler
// wrapper). Returns a nil gRPC server (and mux, unwrapped) when no
// WithGRPCServerFactory calls were made.
func buildHandler(factories []GRPCServerFactory, mux *http.ServeMux) (*grpc.Server, http.Handler, error) {
	grpcServer, err := buildGRPCServer(factories)
	if err != nil {
		return nil, nil, err
	}

	return grpcServer, muxHandler(grpcServer, mux), nil
}

// buildGRPCServer builds a *grpc.Server, registers reflection, then runs
// every factory against it in order. Returns a nil server (and nil error)
// when factories is empty, so callers only pay for gRPC - and the h2c
// listener switch applied in server.go - when WithGRPCServerFactory was
// actually used.
func buildGRPCServer(factories []GRPCServerFactory) (*grpc.Server, error) {
	if len(factories) == 0 {
		return nil, nil //nolint:nilnil // absence of a gRPC server is a valid, common outcome, not an error.
	}

	grpcServer := grpc.NewServer()
	reflection.Register(grpcServer)

	for i, factory := range factories {
		err := factory(grpcServer)
		if err != nil {
			return nil, fmt.Errorf("failed to run gRPC server factory %d: %w", i, err)
		}
	}

	return grpcServer, nil
}

// muxHandler routes a request to grpcServer when its Content-Type
// identifies it as gRPC, otherwise to httpHandler. If grpcServer is nil (no
// WithGRPCServerFactory calls), every request goes to httpHandler.
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
