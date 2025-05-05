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
	return &Server{
		muxServer: &MuxServer{
			cmux:     nil,
			listener: nil,
		},
		httpServer: &HTTPServer{
			server:       nil,
			listener:     nil,
			initializers: []Initializer{},
			mux:          http.NewServeMux(),
		},
		grpcServer: &GRPCServer{
			server:       grpc.NewServer(),
			listener:     nil,
			initializers: []Initializer{},
		},
		logger: zap.L(),
		port:   getPort(ctx),
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	for _, initilizer := range s.httpServer.initializers {
		if err := initilizer(); err != nil {
			s.logger.Error("Failed to initialize HTTP server", zap.Error(err))

			return err
		}
	}

	for _, initilizer := range s.grpcServer.initializers {
		if err := initilizer(); err != nil {
			s.logger.Error("Failed to initialize gRPC server", zap.Error(err))

			return err
		}
	}

	muxListener, err := net.Listen("tcp", ":"+strconv.Itoa(s.port))
	if err != nil {
		return fmt.Errorf("failed to start cmux listener: %w", err)
	}

	s.muxServer.listener = muxListener
	s.muxServer.cmux = cmux.New(muxListener)

	s.grpcServer.listener = s.muxServer.cmux.MatchWithWriters(cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"))
	s.httpServer.listener = s.muxServer.cmux.Match(cmux.Any())

	go s.serveHTTP()
	go s.serveGRPC()

	s.logger.Info("Starting cmux server", zap.Int("port", s.port))
	if err := s.muxServer.cmux.Serve(); err != nil {
		s.logger.Error("Failed to run cmux server", zap.Error(err), zap.Int("port", s.port))
	}

	return nil
}

func (s *Server) serveHTTP() {
	// Wrap the HTTP handler to provide h2c support.
	h2cHandler := h2c.NewHandler(s.httpServer.mux, &http2.Server{})

	s.logger.Info("Starting REST server", zap.Int("port", s.port))

	if err := http.Serve(s.httpServer.listener, h2cHandler); err != nil {
		s.logger.Error("Failed to run REST server", zap.Error(err), zap.Int("port", s.port))
	}
}

func (s *Server) serveGRPC() {
	// Allow reflection to enable tools like grpcurl.
	reflection.Register(s.grpcServer.server)

	s.logger.Info("Starting gRPC server", zap.Int("port", s.port))
	if err := s.grpcServer.server.Serve(s.grpcServer.listener); err != nil {
		s.logger.Error("Failed to run gRPC server", zap.Error(err), zap.Int("port", s.port))
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
