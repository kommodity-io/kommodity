// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kine"
	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/server"
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

	kineServer := kine.NewServer(cfg)

	go func() {
		err = kineServer.StartKine()
		if err != nil {
			logger.Error("Failed to start Kine server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}
	}()

	rootCtx := context.WithoutCancel(ctx)
	kineReadyChan := make(chan struct{})
	kineServer.WaitForKine(ctx, kineReadyChan)

	go func() {
		// Wait for Kine to be ready before starting the API server.
		<-kineReadyChan
		logger.Info("Kine server started successfully")

		server, err := combinedserver.New(combinedserver.ServerConfig{
			Port:        cfg.ServerPort,
			HTTPFactory: server.NewHTTPMuxFactory(rootCtx, cfg),
			GRPCFactory: kms.NewGRPCServerFactory(cfg),
		})
		if err != nil {
			logger.Error("Failed to create combined server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		finalizers = append(finalizers, server.Shutdown)

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
