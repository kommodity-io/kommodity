// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"net/http"
	"os"

	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/kommodity-io/kommodity/pkg/otel"
	"github.com/kommodity-io/kommodity/pkg/server"
	taloskms "github.com/siderolabs/kms-client/api/kms"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

var version = "dev"

func main() {
	ctx := context.Background()

	shutdownFuncs := make([]func(context.Context) error, 0)

	// Configure opentelemetry logger provider.
	loggerProvider := otel.NewLoggerProvider(ctx)
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)

	shutdown := func(c context.Context) {
		for _, shutdownFunc := range shutdownFuncs {
			if err := shutdownFunc(c); err != nil {
				panic(err)
			}
		}
	}

	logger := zap.New(otelzap.NewCore("kommodity", otelzap.WithLoggerProvider(loggerProvider)))
	zap.ReplaceGlobals(logger)

	logger.Info("Starting kommodity server", zap.String("version", version))

	srv := NewServer(ctx)

	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("Failed to start server", zap.Error(err))

		shutdown(ctx)
		os.Exit(1)
	}

	shutdown(ctx)
}

// NewServer create a new kommodity server instance.
func NewServer(ctx context.Context) *server.Server {
	srv := server.New(ctx).
		WithGRPCServerInitializer(func(grpcServer *grpc.Server) error {
			taloskms.RegisterKMSServiceServer(grpcServer, &kms.ServiceServer{})

			return nil
		}).
		WithHTTPMuxInitializer(func(mux *http.ServeMux) error {
			mux.HandleFunc("/readyz", func(res http.ResponseWriter, _ *http.Request) {
				if _, err := res.Write([]byte("OK")); err != nil {
					res.WriteHeader(http.StatusInternalServerError)

					return
				}

				res.WriteHeader(http.StatusOK)
			})

			mux.HandleFunc("/livez", func(res http.ResponseWriter, _ *http.Request) {
				if _, err := res.Write([]byte("OK")); err != nil {
					res.WriteHeader(http.StatusInternalServerError)

					return
				}

				res.WriteHeader(http.StatusOK)
			})

			return nil
		})

	return srv
}
