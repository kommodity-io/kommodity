// Package combinedserver provides a combined gRPC and HTTP server with reverse proxy capabilities.
package combinedserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// HTTPMuxFactory is a function that initializes the HTTP mux.
type HTTPMuxFactory func(*http.ServeMux) error

// GRPCServerFactory is a function that initializes the gRPC server.
type GRPCServerFactory func(*grpc.Server) error

// ServerConfig holds the configuration for the combined server.
type ServerConfig struct {
	GRPCFactory   GRPCServerFactory
	HTTPFactories []HTTPMuxFactory
	Port          int
}

type server struct {
	*ServerConfig

	grpcServer *grpc.Server
	httpMux    *http.ServeMux
	httpServer *http.Server
}

// New creates a new combined server with gRPC listener and HTTP proxy.
//
//nolint:revive
func New(config ServerConfig) (*server, error) {
	return &server{
		ServerConfig: &config,
	}, nil
}

// ListenAndServe starts the combined server.
func (s *server) ListenAndServe(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	// Initialize gRPC server
	s.grpcServer = grpc.NewServer()
	reflection.Register(s.grpcServer)

	err := s.GRPCFactory(s.grpcServer)
	if err != nil {
		return fmt.Errorf("failed to create gRPC factory: %w", err)
	}

	// Initialize HTTP mux
	s.httpMux = http.NewServeMux()
	for _, factory := range s.HTTPFactories {
		err := factory(s.httpMux)
		if err != nil {
			return fmt.Errorf("failed to create HTTP mux: %w", err)
		}
	}

	// Create a handler that routes based on Content-Type header.
	// gRPC requests have Content-Type starting with "application/grpc".
	// This allows both gRPC and HTTP to be served on the same port,
	// which is necessary when running behind a reverse proxy that
	// terminates TLS and forwards HTTP/2.
	mixedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/grpc") {
			s.grpcServer.ServeHTTP(w, r)
		} else {
			s.httpMux.ServeHTTP(w, r)
		}
	})

	// Create HTTP server with h2c support for HTTP/2 without TLS
	s.httpServer = &http.Server{
		Addr:              ":" + strconv.Itoa(s.Port),
		Handler:           h2c.NewHandler(mixedHandler, &http2.Server{}),
		ReadHeaderTimeout: 1 * time.Second,
	}

	logger.Info("Starting combined HTTP/gRPC server", zap.Int("port", s.Port))

	err = s.httpServer.ListenAndServe()
	if err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("Server closed", zap.Int("port", s.Port))

			return nil
		}

		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func (s *server) Shutdown(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	if s.grpcServer != nil {
		logger.Info("Shutting down gRPC server", zap.Int("port", s.Port))
		s.grpcServer.GracefulStop()
		logger.Info("Shut down gRPC server", zap.Int("port", s.Port))
	}

	if s.httpServer != nil {
		s.httpServer.SetKeepAlivesEnabled(false)

		logger.Info("Shutting down HTTP server", zap.Int("port", s.Port))

		err := s.httpServer.Shutdown(ctx)
		if err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %w", err)
		}

		logger.Info("Shut down HTTP server", zap.Int("port", s.Port))
	}

	return nil
}
