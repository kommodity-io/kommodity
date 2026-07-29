package logging_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kommodity-io/kommodity/pkg/libkapi/logging"
)

// These tests must NOT use t.Parallel: logrus's standard logger is
// process-global and InstallLogrusAdapter mutates it (hooks, output,
// level, ExitFunc). Running in parallel with sibling tests would race
// on the global state.

// TestInstallLogrusAdapter_RoutesInfo verifies that logrus.Info calls
// are forwarded to the slog logger at Info level.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_RoutesInfo(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	logrus.Info("database tables are up to date")

	output := buf.String()
	assert.Contains(t, output, "database tables are up to date")
	assert.Contains(t, output, "level=INFO")
	assert.Contains(t, output, "component=kine")
}

// TestInstallLogrusAdapter_RoutesWarn verifies that logrus.Warn calls
// are forwarded to the slog logger at Warn level.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_RoutesWarn(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	logrus.Warn("compaction took longer than expected")

	output := buf.String()
	assert.Contains(t, output, "compaction took longer than expected")
	assert.Contains(t, output, "level=WARN")
	assert.Contains(t, output, "component=kine")
}

// TestInstallLogrusAdapter_RoutesError verifies that logrus.Error calls
// are forwarded to the slog logger at Error level.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_RoutesError(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	logrus.Error("failed to list latest changes")

	output := buf.String()
	assert.Contains(t, output, "failed to list latest changes")
	assert.Contains(t, output, "level=ERROR")
	assert.Contains(t, output, "component=kine")
}

// TestInstallLogrusAdapter_FatalDoesNotExit verifies that logrus.Fatal
// calls are logged at Error level but do NOT call os.Exit — the
// ExitFunc is neutralized so kine's MustCommit/MustRollback don't kill
// the process on a momentary DB outage.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_FatalDoesNotExit(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	require.NotPanics(t, func() {
		logrus.Fatal("transaction commit failed")
	})

	output := buf.String()
	assert.Contains(t, output, "transaction commit failed",
		"Fatal messages should be logged at Error level")
	assert.Contains(t, output, "level=ERROR",
		"Fatal messages should be logged at Error level")
}

// TestInstallLogrusAdapter_SuppressesOriginalOutput verifies that
// logrus's own output is suppressed — the hook routes to slog, and
// the original stderr writer is discarded so messages don't appear
// twice.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_SuppressesOriginalOutput(t *testing.T) {
	var buf bytes.Buffer

	// Capture what logrus would write to its output.
	var logrusBuf bytes.Buffer

	logrus.SetOutput(&logrusBuf)
	t.Cleanup(func() { logrus.SetOutput(nil) }) // restore default

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	logrus.Info("kine available at unix:///tmp/kine.sock")

	// slog output should contain the message.
	assert.Contains(t, buf.String(), "kine available")

	// logrus's own output should be empty (discarded).
	assert.Empty(t, logrusBuf.String(),
		"logrus should not write to its own output after adapter install")
}

// TestInstallLogrusAdapter_RoutesFields verifies that logrus structured
// fields are forwarded as slog key-value pairs.
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_RoutesFields(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logging.InstallLogrusAdapter(logger)

	logrus.WithField("endpoint", "postgres://localhost:5432").Info("connected")

	output := buf.String()
	assert.Contains(t, output, "connected")
	assert.Contains(t, output, "endpoint=postgres://localhost:5432")
}

// TestInstallLogrusAdapter_NilLoggerFallsBackToDefault verifies that
// passing a nil logger does not panic and falls back to slog.Default().
//
//nolint:paralleltest // mutates process-global logrus state
func TestInstallLogrusAdapter_NilLoggerFallsBackToDefault(t *testing.T) {
	originalDefault := slog.Default()

	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	var buf bytes.Buffer

	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	require.NotPanics(t, func() {
		logging.InstallLogrusAdapter(nil)
	})

	logrus.Info("nil-fallback info")

	assert.Contains(t, buf.String(), "nil-fallback info",
		"Info should reach slog.Default when nil is passed")
}
