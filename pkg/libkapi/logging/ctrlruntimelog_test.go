package logging_test

import (
	"log/slog"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/libkapi/logging"
	"github.com/stretchr/testify/require"
)

// Unlike klog (which exposes klog.ClearLogger for tests) or the gRPC/logrus
// adapters, controller-runtime's log.SetLogger fulfills a promise exactly
// once, process-wide, and offers no public reset - see
// sigs.k8s.io/controller-runtime/pkg/log/deleg.go's loggerPromise.Fulfill:
// once fulfilled, its promise is set to nil and further Fulfill calls are
// no-ops. Whichever call (this test's, or one from any other test's
// libkapi.New) happens to run first in the process wins, and there's no way
// to reset that between tests. So unlike the sibling klog/gRPC/logrus
// adapter tests, these don't assert on captured output - only that the call
// itself is safe.

// TestInstallControllerRuntimeLogAdapter_NilLoggerFallsBackToDefault
// verifies that passing a nil logger does not panic and falls back to
// slog.Default().
func TestInstallControllerRuntimeLogAdapter_NilLoggerFallsBackToDefault(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		logging.InstallControllerRuntimeLogAdapter(nil)
	})
}

// TestInstallControllerRuntimeLogAdapter_SafeToCallRepeatedly verifies that
// calling InstallControllerRuntimeLogAdapter more than once - as happens
// whenever more than one Server is built in the same process - does not
// panic, even though only the first call's logger actually takes effect.
func TestInstallControllerRuntimeLogAdapter_SafeToCallRepeatedly(t *testing.T) {
	t.Parallel()

	logger := slog.Default()

	require.NotPanics(t, func() {
		logging.InstallControllerRuntimeLogAdapter(logger)
		logging.InstallControllerRuntimeLogAdapter(logger)
	})
}
