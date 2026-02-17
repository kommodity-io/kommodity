// Package combinedserver provides a combined gRPC and HTTP server with reverse proxy capabilities.
package combinedserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// ServerState represents the current state of the server.
type ServerState int32

const (
	// ServerStateStarting indicates the server is initializing.
	ServerStateStarting ServerState = iota
	// ServerStateRunning indicates the server is running and ready to accept requests.
	ServerStateRunning
	// ServerStateShuttingDown indicates the server is shutting down.
	ServerStateShuttingDown
)

// String returns a human-readable representation of the server state.
func (s ServerState) String() string {
	switch s {
	case ServerStateStarting:
		return "starting"
	case ServerStateRunning:
		return "running"
	case ServerStateShuttingDown:
		return "shutting down"
	default:
		return "unknown"
	}
}

// ServerStateTracker tracks the server's lifecycle state.
type ServerStateTracker struct {
	state atomic.Int32
}

// NewServerStateTracker creates a new server state tracker initialized to Starting state.
func NewServerStateTracker() *ServerStateTracker {
	tracker := &ServerStateTracker{}
	tracker.state.Store(int32(ServerStateStarting))

	return tracker
}

// SetState updates the server state.
func (t *ServerStateTracker) SetState(state ServerState) {
	t.state.Store(int32(state))
}

// GetState returns the current server state.
func (t *ServerStateTracker) GetState() ServerState {
	return ServerState(t.state.Load())
}

// IsRunning returns true if the server is in the running state.
func (t *ServerStateTracker) IsRunning() bool {
	return t.GetState() == ServerStateRunning
}

// HTTPMuxFactory is a function that initializes the HTTP mux.
type HTTPMuxFactory func(*http.ServeMux) error

// GRPCServerFactory is a function that initializes the gRPC server.
type GRPCServerFactory func(*grpc.Server) error

// ServerConfig holds the configuration for the combined server.
type ServerConfig struct {
	GRPCFactory   GRPCServerFactory
	HTTPFactories []HTTPMuxFactory
	Port          int
	// APIServerPort is the port where the internal Kubernetes API server listens.
	// Used for health checks to verify API server readiness.
	APIServerPort int
}

type server struct {
	*ServerConfig

	grpcServer   *grpc.Server
	httpMux      *http.ServeMux
	httpServer   *http.Server
	stateTracker *ServerStateTracker
}

// New creates a new combined server with gRPC listener and HTTP proxy.
//
//nolint:revive
func New(config ServerConfig) (*server, error) {
	return &server{
		ServerConfig: &config,
		stateTracker: NewServerStateTracker(),
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

	// Register unauthenticated health check endpoints first
	registerHealthChecks(s.httpMux, s.stateTracker, HealthCheckConfig{
		APIServerPort: s.APIServerPort,
	})

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

	// Mark server as running before starting to listen
	s.stateTracker.SetState(ServerStateRunning)

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

	// Mark server as shutting down
	s.stateTracker.SetState(ServerStateShuttingDown)

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
