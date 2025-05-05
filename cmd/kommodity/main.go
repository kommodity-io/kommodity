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

func main() {
	ctx := context.Background()

	// Configure opentelemetry logger provider.
	loggerProvider := otel.NewLoggerProvider(ctx)
	defer loggerProvider.Shutdown(ctx)

	logger := zap.New(otelzap.NewCore("kommodity", otelzap.WithLoggerProvider(loggerProvider)))
	zap.ReplaceGlobals(logger)

	srv := NewServer(ctx)

	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("Failed to start server", zap.Error(err))

		loggerProvider.Shutdown(ctx)
		os.Exit(1)
	}
}

func NewServer(ctx context.Context) *server.Server {
	srv := server.New(ctx).
		WithGRPCServerInitializer(func(grpcServer *grpc.Server) error {
			taloskms.RegisterKMSServiceServer(grpcServer, &kms.KMSServiceServer{})

			return nil
		}).
		WithHTTPMuxInitializer(func(mux *http.ServeMux) error {
			mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("OK"))
			})

			mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("OK"))
			})

			return nil
		})

	return srv
}
