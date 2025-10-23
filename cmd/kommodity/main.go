// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	attestationserver "github.com/kommodity-io/kommodity/pkg/attestation"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kine"
	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/kommodity-io/kommodity/pkg/logging"
	k8sserver "github.com/kommodity-io/kommodity/pkg/server"
	"go.uber.org/zap"
	genericapiserver "k8s.io/apiserver/pkg/server"

	_ "github.com/joho/godotenv/autoload"
)

//nolint:funlen // Not complex enough to warrant breaking down, only initialization logic and goroutines.
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

	cfg, err := config.LoadConfig(ctx)
	if err != nil {
		logger.Error("Failed to load config", zap.Error(err))

		return
	}

	server, err := combinedserver.New(combinedserver.ServerConfig{
		Port: cfg.ServerPort,
		HTTPFactories: []combinedserver.HTTPMuxFactory{
			attestationserver.NewHTTPMuxFactory(cfg),
			k8sserver.NewHTTPMuxFactory(ctx, cfg),
		},
		GRPCFactory: kms.NewGRPCServerFactory(cfg),
	})
	if err != nil {
		logger.Error("Failed to create combined server", zap.Error(err))

		// Ensure that the server is shut down gracefully when an error occurs.
		signals <- syscall.SIGTERM

		return
	}

	finalizers = append(finalizers, server.Shutdown)

	go func() {
		err = kine.StartKine(cfg)
		if err != nil {
			logger.Error("Failed to start Kine server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}
	}()

	go func() {
		err = server.ListenAndServe(ctx)
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
