// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/server"
	"go.uber.org/zap"
	genericapiserver "k8s.io/apiserver/pkg/server"

	_ "github.com/joho/godotenv/autoload"
)

var (
	version = "dev"
	//nolint:gochecknoglobals // commit is set by the build system to the git commit hash.
	commit = "unknown"
	//nolint:gochecknoglobals // buildDate is set by the build system to the build date.
	buildDate = "unknown"
)

func main() {
	logger := logging.NewLogger()
	ctx := logging.WithLogger(genericapiserver.SetupSignalContext(), logger)

	triggers := []os.Signal{
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGPIPE,
		syscall.SIGTERM,
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, triggers...)
	signal.NotifyContext(ctx, triggers...)

	finalizers := make([]func(context.Context) error, 0)

	// Configure the zap OTEL logger.
	zap.ReplaceGlobals(logger)

	logger.Info("Starting kommodity server",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("buildDate", buildDate),
	)

	go func() {
		srv, err := combinedserver.New(combinedserver.ServerConfig{
			Port:        config.GetServerPort(ctx),
			HTTPFactory: server.NewHTTPMuxFactory(ctx),
			GRPCFactory: kms.NewGRPCServerFactory(),
		})
		if err != nil {
			logger.Error("Failed to create combined server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		err = srv.ListenAndServe(ctx)
		if err != nil {
			logger.Error("Failed to run combined server", zap.Error(err))
		}

		logger.Info("API Server started successfully")
	}()

	sig := <-signals
	logger.Info("Received signal", zap.String("signal", sig.String()))

	// Call the finalizers in reverse order.
	for i := len(finalizers) - 1; i >= 0; i-- {
		err := finalizers[i](ctx)
		if err != nil {
			logger.Error("Failed to shutdown", zap.Error(err))
		}
	}
}
