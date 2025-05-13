// Package server contains the plumbing for a server
// that can handle both gRPC and REST requests.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kommodity-io/kommodity/pkg/encoding"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Factory is a function that initializes the server.
type Factory func() error

// HTTPMuxFactory is a function that initializes the HTTP mux.
type HTTPMuxFactory func(*http.ServeMux) error

// GRPCServerFactory is a function that initializes the gRPC server.
type GRPCServerFactory func(*grpc.Server) error

// HTTPServer is a struct that contains the HTTP server configuration.
type HTTPServer struct {
	server    *http.Server
	listener  net.Listener
	mux       *http.ServeMux
	factories []Factory
}

// GRPCServer is a struct that contains the gRPC server configuration.
type GRPCServer struct {
	server    *grpc.Server
	listener  net.Listener
	factories []Factory
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
	ready      bool
	sync.RWMutex
}

// New creates a new server instance.
func New(ctx context.Context, opts ...Option) *Server {
	srv := &Server{
		muxServer: &MuxServer{
			cmux:     nil,
			listener: nil,
		},
		httpServer: &HTTPServer{
			server:    nil,
			listener:  nil,
			factories: []Factory{},
			mux:       http.NewServeMux(),
		},
		grpcServer: &GRPCServer{
			server:    grpc.NewServer(),
			listener:  nil,
			factories: []Factory{},
		},
		logger: zap.L(),
		port:   getPort(ctx),
	}

	for _, opt := range opts {
		opt(srv)
	}

	return srv
}

// ListenAndServe starts the server and listens for incoming requests.
// It initializes the HTTP and gRPC servers and starts the cmux server.
// The HTTP server is wrapped with h2c support to allow HTTP/2 connections.
// The gRPC server is registered with reflection to allow for introspection.
func (s *Server) ListenAndServe(_ context.Context) error {
	for _, factory := range s.httpServer.factories {
		if err := factory(); err != nil {
			s.logger.Error("Failed to initialize HTTP server", zap.Error(err))

			return err
		}
	}

	for _, factory := range s.grpcServer.factories {
		if err := factory(); err != nil {
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

	s.grpcServer.listener = s.muxServer.cmux.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"),
	)
	s.httpServer.listener = s.muxServer.cmux.Match(cmux.Any())

	go s.serveHTTP()
	go s.serveGRPC()

	s.SetReady(true)

	s.logger.Info("Starting cmux server", zap.Int("port", s.port))

	if err := s.muxServer.cmux.Serve(); err != nil {
		// This is expected when the server is shut down gracefully.
		// Reference: https://github.com/soheilhy/cmux/pull/92
		if !errors.Is(err, net.ErrClosed) {
			s.logger.Error("Failed to run cmux server", zap.Error(err), zap.Int("port", s.port))

			return fmt.Errorf("failed to run cmux server: %w", err)
		}

		s.logger.Info("Closed cmux listener", zap.Int("port", s.port))
	}

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.SetReady(false)

	if s.muxServer.cmux != nil {
		s.logger.Info("Shutting down cmux server", zap.Int("port", s.port))

		s.muxServer.cmux.Close()
	}

	if s.grpcServer.server != nil {
		s.logger.Info("Shutting down gRPC server", zap.Int("port", s.port))

		s.grpcServer.server.GracefulStop()

		s.logger.Info("Shut down gRPC server", zap.Int("port", s.port))
	}

	if s.httpServer.server != nil {
		s.httpServer.server.SetKeepAlivesEnabled(false)

		s.logger.Info("Shutting down HTTP server", zap.Int("port", s.port))

		if err := s.httpServer.server.Shutdown(ctx); err != nil {
			// This is expected when the server is shut down via cmux.
			// Reference: https://github.com/soheilhy/cmux/pull/92
			if errors.Is(err, net.ErrClosed) {
				s.logger.Info("Shut down HTTP server", zap.Int("port", s.port))

				return nil
			}
		}

		s.logger.Info("Shut down HTTP server", zap.Int("port", s.port))
	}

	return nil
}

// SetReady sets the server's readiness state.
func (s *Server) SetReady(ready bool) {
	s.Lock()
	defer s.Unlock()

	s.ready = ready
}

func (s *Server) serveHTTP() {
	s.httpServer.mux.HandleFunc("/readyz", s.readyz)
	s.httpServer.mux.HandleFunc("/livez", s.livez)

	s.httpServer.server = &http.Server{
		// Wrap the HTTP handler to provide h2c support.
		Handler: h2c.NewHandler(s.httpServer.mux, &http2.Server{}),
		// This prevents slowloris attacks, but may be rather aggressive.
		ReadHeaderTimeout: 1 * time.Second,
	}

	s.logger.Info("Starting HTTP server", zap.Int("port", s.port))

	if err := s.httpServer.server.Serve(s.httpServer.listener); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// This is expected when the server is shut down gracefully.
			return
		}

		if errors.Is(err, cmux.ErrServerClosed) {
			// This is expected when the server is shut down via cmux.
			return
		}

		s.logger.Error("Failed to run HTTP server", zap.Error(err), zap.Int("port", s.port))
	}
}

func (s *Server) serveGRPC() {
	// Allow reflection to enable tools like grpcurl.
	reflection.Register(s.grpcServer.server)

	s.logger.Info("Starting gRPC server", zap.Int("port", s.port))

	if err := s.grpcServer.server.Serve(s.grpcServer.listener); err != nil {
		// This is expected when the server is shut down gracefully.
		if !errors.Is(err, grpc.ErrServerStopped) {
			s.logger.Error("Failed to run gRPC server", zap.Error(err), zap.Int("port", s.port))
		}
	}
}

func (s *Server) readyz(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	if !s.ready {
		code := http.StatusServiceUnavailable
		status := &metav1.Status{
			Status:  "Failure",
			Code:    int32(code),
			Message: "Not ready to serve requests",
			Reason:  metav1.StatusReason(http.StatusText(code)),
		}

		res.WriteHeader(code)

		if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
			s.logger.Error("Failed to encode status", zap.Error(err))

			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

			return
		}

		return
	}

	code := http.StatusOK
	status := &metav1.Status{
		Status:  "Success",
		Code:    int32(code),
		Reason:  metav1.StatusReason(http.StatusText(code)),
		Message: "Ready to serve requests",
	}

	res.WriteHeader(code)

	if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
		s.logger.Error("Failed to encode status", zap.Error(err))

		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

		return
	}
}

func (s *Server) livez(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	code := http.StatusOK
	status := &metav1.Status{
		Status:  "Success",
		Code:    int32(code),
		Reason:  metav1.StatusReason(http.StatusText(code)),
		Message: "Server running",
	}

	res.WriteHeader(code)

	if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
		s.logger.Error("Failed to encode status", zap.Error(err))

		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

		return
	}
}
