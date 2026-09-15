package libkapi

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"
	restclient "k8s.io/client-go/rest"
)

// ServerFactory extends the built server using whatever combination of
// Ctx's resources it needs: register gRPC services via Ctx.GRPCServer,
// mount HTTP routes via Ctx.HTTPMux, build a privileged client from
// Ctx.LoopbackConfig, and/or dial the storage backend directly via
// Ctx.StorageEndpoints. It replaces the narrower GRPCServerFactory and
// HTTPHandlerFactory, which remain as deprecated adapters implemented in
// terms of WithServerFactory.
type ServerFactory func(*Ctx) error

// WithServerFactory registers factory to run once, while the server is
// being built, against a shared Ctx exposing the HTTP mux, the gRPC server
// (built lazily - see Ctx.GRPCServer), the server's own loopback client
// config, and its storage backend's endpoints. Can be passed more than
// once; factories run in registration order against the same Ctx.
func WithServerFactory(factory ServerFactory) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.serverFactories = append(cfg.serverFactories, factory)

		return nil
	}
}

// runServerFactories runs every registered ServerFactory (including those
// registered through the deprecated WithGRPCServerFactory/
// WithHTTPHandlerFactory adapters) against one shared Ctx, then mounts
// apiHandler as the mux's fallback route. Returns the gRPC server (nil
// unless some factory called Ctx.GRPCServer) alongside the handler
// buildServer should actually serve: muxHandler routes gRPC-Content-Type
// requests to it when non-nil. The listener's own h2c switch is applied
// separately, by server.go's newHTTPServer, based on whether the returned
// gRPC server is nil.
func runServerFactories(
	factories []ServerFactory,
	loopbackConfig *restclient.Config,
	storageEndpoints []string,
	apiHandler http.Handler,
) (*grpc.Server, http.Handler, error) {
	factoryCtx := &Ctx{
		mux:              http.NewServeMux(),
		loopbackConfig:   loopbackConfig,
		storageEndpoints: storageEndpoints,
	}

	for i, factory := range factories {
		err := factory(factoryCtx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to run server factory %d: %w", i, err)
		}
	}

	factoryCtx.mux.Handle("/", apiHandler)

	return factoryCtx.grpcServer, muxHandler(factoryCtx.grpcServer, factoryCtx.mux), nil
}
