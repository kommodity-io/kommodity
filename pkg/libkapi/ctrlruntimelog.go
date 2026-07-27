package libkapi

import (
	"log/slog"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// InstallControllerRuntimeLogAdapter bridges controller-runtime's global
// logr sink to the consumer's slog logger, so that any
// sigs.k8s.io/controller-runtime usage in the process - not just the
// Manager libkapi itself builds for WithController - logs through logger
// instead of printing "log.SetLogger(...) was never called; logs will not
// be displayed" and silently discarding its output. This matters for code
// that talks to controller-runtime directly, e.g. a plain client.New(...)
// call made outside any Controller's SetupWithManager.
//
// The bridge installs a process-wide global: controller-runtime has a
// single backing logger, so the last call wins. New calls
// InstallControllerRuntimeLogAdapter during server construction, before any
// goroutine starts logging - the same point it installs the klog/gRPC/
// logrus bridges.
//
// If logger is nil, slog.Default() is used, matching WithLogger's fallback
// behavior.
func InstallControllerRuntimeLogAdapter(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	ctrl.SetLogger(logr.FromSlogHandler(logger.With("component", "controller-runtime").Handler()))
}
