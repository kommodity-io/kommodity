// Package server contains the plumbing for a server
// that can handle both gRPC and REST requests.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Initializer is a function that initializes the server.
type Initializer func() error

// MuxInitializer is a function that initializes the HTTP mux.
type MuxInitializer func(*http.ServeMux) error

// GRPCInitializer is a function that initializes the gRPC server.
type GRPCInitializer func(*grpc.Server) error

// HTTPServer is a struct that contains the HTTP server configuration.
type HTTPServer struct {
	server       *http.Server
	listener     net.Listener
	mux          *http.ServeMux
	initializers []Initializer
}

// GRPCServer is a struct that contains the gRPC server configuration.
type GRPCServer struct {
	server       *grpc.Server
	listener     net.Listener
	initializers []Initializer
}

// MuxServer is a struct that contains the cmux server configuration.
type MuxServer struct {
	cmux     cmux.CMux
	listener net.Listener
}

// Server is a struct that contains the server configuration.
type Server struct {
	muxServer  *MuxServer
	grpcServer *GRPCServer
	httpServer *HTTPServer
	logger     *zap.Logger
	port       int
}

// New creates a new server instance.
func New(ctx context.Context) *Server {
	logger := zap.L()

	port := getPort(ctx)

	muxListener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		logger.Fatal("Failed to start listener", zap.Error(err), zap.Int("port", port))
	}

	multiplexer := cmux.New(muxListener)

	grpcListener := multiplexer.MatchWithWriters(cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"))
	httpListener := multiplexer.Match(cmux.Any())

	return &Server{
		muxServer: &MuxServer{
			cmux:     multiplexer,
			listener: muxListener,
		},
		httpServer: &HTTPServer{
			server:       &http.Server{},
			listener:     httpListener,
			mux:          http.NewServeMux(),
			initializers: []Initializer{},
		},
		grpcServer: &GRPCServer{
			server:       grpc.NewServer(),
			listener:     grpcListener,
			initializers: []Initializer{},
		},
		logger: logger,
		port:   port,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	logger := zap.L()

	for _, initilizer := range s.httpServer.initializers {
		if err := initilizer(); err != nil {
			logger.Error("Failed to initialize HTTP server", zap.Error(err))

			return err
		}
	}

	for _, initilizer := range s.grpcServer.initializers {
		if err := initilizer(); err != nil {
			logger.Error("Failed to initialize gRPC server", zap.Error(err))

			return err
		}
	}

	go s.serveHTTP(logger)
	go s.serveGRPC(logger)

	logger.Info("Starting cmux server", zap.Int("port", s.port))
	if err := s.muxServer.cmux.Serve(); err != nil {
		logger.Error("Failed to run cmux server", zap.Error(err), zap.Int("port", s.port))
	}

	return nil
}

func (s *Server) serveHTTP(logger *zap.Logger) {
	// Wrap the HTTP handler to provide h2c support.
	h2cHandler := h2c.NewHandler(s.httpServer.mux, &http2.Server{})

	logger.Info("Starting REST server", zap.Int("port", s.port))

	if err := http.Serve(s.httpServer.listener, h2cHandler); err != nil {
		logger.Error("Failed to run REST server", zap.Error(err), zap.Int("port", s.port))
	}
}

func (s *Server) serveGRPC(logger *zap.Logger) {
	// Allow reflection to enable tools like grpcurl.
	reflection.Register(s.grpcServer.server)

	logger.Info("Starting gRPC server", zap.Int("port", s.port))
	if err := s.grpcServer.server.Serve(s.grpcServer.listener); err != nil {
		logger.Error("Failed to run gRPC server", zap.Error(err), zap.Int("port", s.port))
	}
}

// WithHTTPMuxInitializer registers a HTTP service.
func (s *Server) WithHTTPMuxInitializer(initialize MuxInitializer) *Server {
	s.httpServer.initializers = append(s.httpServer.initializers, func() error {
		err := initialize(s.httpServer.mux)
		if err != nil {
			return fmt.Errorf("failed to run HTTP mux initializer: %w", err)
		}

		return nil
	})

	return s
}

// WithGRPCServerInitializer registers a gRPC service.
func (s *Server) WithGRPCServerInitializer(initialize GRPCInitializer) *Server {
	s.grpcServer.initializers = append(s.grpcServer.initializers, func() error {
		err := initialize(s.grpcServer.server)
		if err != nil {
			return fmt.Errorf("failed to run gRPC server initializer: %w", err)
		}

		return nil
	})

	return s
}
