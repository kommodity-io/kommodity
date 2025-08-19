// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kommodity-io/kommodity/pkg/apiserver"
	"github.com/kommodity-io/kommodity/pkg/controller"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	genericapiserver "k8s.io/apiserver/pkg/server"

	_ "github.com/joho/godotenv/autoload"
)

//nolint:funlen
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

	go func() {
		srv, err := apiserver.New()
		if err != nil {
			logger.Error("Failed to create server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		preparedGenericServer := srv.PrepareRun()

		err = preparedGenericServer.RunWithContext(ctx)
		if err != nil {
			logger.Error("Failed to run generic server", zap.Error(err))
		}

		logger.Info("API Server started successfully")
	}()

	go func() {
		ctlMgr, err := controller.NewAggregatedControllerManager(ctx)
		if err != nil {
			logger.Error("Failed to create controller manager", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		err = ctlMgr.Start(ctx)
		if err != nil {
			logger.Error("Failed to start controller manager", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		logger.Info("Controller manager started successfully")
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
