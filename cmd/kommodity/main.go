// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/kommodity-io/kommodity/pkg/otel"
	"github.com/kommodity-io/kommodity/pkg/server"
	"github.com/soheilhy/cmux"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

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

	// Configure opentelemetry logger provider.
	loggerProvider := otel.NewLoggerProvider(ctx)
	finalizers = append(finalizers, loggerProvider.Shutdown)

	// Configure the zap OTEL logger.
	logger := zap.New(otelzap.NewCore("kommodity", otelzap.WithLoggerProvider(loggerProvider)))
	zap.ReplaceGlobals(logger)

	logger.Info("Starting kommodity server", zap.String("version", version))

	go func() {
		srv, err := server.New(ctx)
		if err != nil {
			logger.Error("Failed to create server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		finalizers = append(finalizers, srv.Shutdown)

		if err := srv.ListenAndServe(ctx); err != nil {
			// This is expected as part of the shutdown process.
			// Reference: https://github.com/soheilhy/cmux/issues/39
			if errors.Is(err, cmux.ErrListenerClosed) {
				return
			}

			logger.Error("Failed to run cmux server", zap.Error(err))
		}
	}()

	sig := <-signals
	logger.Info("Received signal", zap.String("signal", sig.String()))

	// Call the finalizers in reverse order.
	for i := len(finalizers) - 1; i >= 0; i-- {
		if err := finalizers[i](ctx); err != nil {
			logger.Error("Failed to shutdown", zap.Error(err))
		}
	}
}
