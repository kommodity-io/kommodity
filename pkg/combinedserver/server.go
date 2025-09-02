// Package combinedserver provides a combined gRPC and HTTP server with reverse proxy capabilities.
package combinedserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/soheilhy/cmux"
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
	GRPCFactory GRPCServerFactory
	HTTPFactory HTTPMuxFactory
	Port        int
}

type server struct {
	*ServerConfig

	cmuxServer   cmux.CMux
	grpcListener net.Listener
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

	listenerConfig := net.ListenConfig{}

	muxListener, err := listenerConfig.Listen(ctx, "tcp", ":"+strconv.Itoa(s.Port))
	if err != nil {
		return fmt.Errorf("failed to start cmux listener: %w", err)
	}

	s.cmuxServer = cmux.New(muxListener)
	s.grpcListener = s.cmuxServer.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"),
	)

	go func() {
		runCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)

		err := s.serveHTTP(runCtx)
		if err != nil {
			cancel(fmt.Errorf("failed to start HTTP server: %w", err))
		}
	}()

	go func() {
		runCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)

		err := s.serveGRPC(runCtx)
		if err != nil {
			cancel(fmt.Errorf("failed to start gRPC server: %w", err))
		}
	}()

	err = s.cmuxServer.Serve()
	if err != nil {
		// This is expected when the server is shut down gracefully.
		// Reference: https://github.com/soheilhy/cmux/pull/92
		if !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("failed to run cmux server: %w", err)
		}

		logger.Info("Closed cmux listener", zap.Int("port", s.Port))
	}

	return nil
}

func (s *server) serveHTTP(_ context.Context) error {
	listener := s.cmuxServer.Match(cmux.Any())
	httpMux := http.NewServeMux()

	httpServer := &http.Server{
		Handler:           h2c.NewHandler(httpMux, &http2.Server{}),
		ReadHeaderTimeout: 1 * time.Second,
	}

	err := s.HTTPFactory(httpMux)
	if err != nil {
		return fmt.Errorf("failed to create HTTP mux: %w", err)
	}

	err = httpServer.Serve(listener)
	if err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// This is expected when the server is shut down gracefully.
			return nil
		}

		if errors.Is(err, cmux.ErrServerClosed) {
			// This is expected when the server is shut down via cmux.
			return nil
		}

		return fmt.Errorf("failed to serve HTTP: %w", err)
	}

	return nil
}

func (s *server) serveGRPC(_ context.Context) error {
	grpcServer := grpc.NewServer()

	// Allow reflection to enable tools like grpcurl.
	reflection.Register(grpcServer)

	err := s.GRPCFactory(grpcServer)
	if err != nil {
		return fmt.Errorf("failed to create gRPC factory: %w", err)
	}

	err = grpcServer.Serve(s.grpcListener)
	if err != nil {
		// This is expected when the server is shut down gracefully.
		if !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("failed to serve gRPC: %w", err)
		}
	}

	return nil
}
