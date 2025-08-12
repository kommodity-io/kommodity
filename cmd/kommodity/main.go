// Package main provides the main entry point for the kommodity server.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kommodity-io/kommodity/pkg/apiserver"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	genericapiserver "k8s.io/apiserver/pkg/server"
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
		srv, err := apiserver.New(ctx)
		if err != nil {
			logger.Error("Failed to create server", zap.Error(err))

			// Ensure that the server is shut down gracefully when an error occurs.
			signals <- syscall.SIGTERM

			return
		}

		preparedGenericServer := srv.PrepareRun()
		preparedGenericServer.RunWithContext(ctx)

		// // Set the server version.
		// srv.SetVersion(&kubeversion.Info{
		// 	GitVersion: version,
		// 	GitCommit:  commit,
		// 	BuildDate:  buildDate,
		// })

		// finalizers = append(finalizers, srv.Shutdown)

		// err = srv.ListenAndServe(ctx)
		// if err != nil {
		// 	// This is expected as part of the shutdown process.
		// 	// Reference: https://github.com/soheilhy/cmux/issues/39
		// 	if errors.Is(err, cmux.ErrListenerClosed) {
		// 		return
		// 	}

		// 	logger.Error("Failed to run cmux server", zap.Error(err))
		// }
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
