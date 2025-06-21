package genericserver

import (
	"fmt"

	"k8s.io/apimachinery/pkg/version"
)

// Option is a function that configures the server.
type Option func(*GenericServer)

// WithGRPCServerFactory adds a gRPC factory to the server.
func WithGRPCServerFactory(factory GRPCServerFactory) Option {
	return func(s *GenericServer) {
		s.grpcServer.factories = append(s.grpcServer.factories, func() error {
			if err := factory(s.grpcServer.server); err != nil {
				return fmt.Errorf("failed to run gRPC server factory: %w", err)
			}

			return nil
		})
	}
}

// WithHTTPMuxFactory adds a HTTP mux factory to the server.
func WithHTTPMuxFactory(factory HTTPMuxFactory) Option {
	return func(s *GenericServer) {
		s.httpServer.factories = append(s.httpServer.factories, func() error {
			if err := factory(s.httpServer.mux); err != nil {
				return fmt.Errorf("failed to run HTTP factory: %w", err)
			}

			return nil
		})
	}
}

// WithVersionInfo sets the version information for the server.
func WithVersionInfo(versionInfo *version.Info) Option {
	return func(s *GenericServer) {
		s.versionInfo = versionInfo
	}
}
