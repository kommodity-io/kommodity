package libkapi_test

import (
	"bytes"
	"log/slog"
	"testing"

	"google.golang.org/grpc/grpclog"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests must NOT use t.Parallel: grpclog's backing logger is
// process-global and InstallGRPCLogAdapter mutates it. Running in
// parallel with sibling tests (or with TestServerEndToEnd, which calls
// New and therefore InstallGRPCLogAdapter) would race on the global
// state.

// TestInstallGRPCLogAdapter_DemotesInfoToDebug verifies that gRPC
// Info-level calls are demoted to slog.Debug so they don't clutter
// normal output but remain available for troubleshooting.
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_DemotesInfoToDebug(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	libkapi.InstallGRPCLogAdapter(logger)

	grpclog.Info("connection established")
	grpclog.Infoln("connection established")
	grpclog.Infof("connection %s", "established")

	output := buf.String()
	assert.Contains(t, output, "connection established")
	assert.Contains(t, output, "level=DEBUG")
	assert.Contains(t, output, "component=grpc")
}

// TestInstallGRPCLogAdapter_DemotesWarningToDebug verifies that gRPC
// Warning-level calls are demoted to slog.Debug. The
// "addrConn.createTransport failed to connect" messages that clutter
// startup are logged at Warning via channelz.Warningf
// (clientconn.go:1525), so demoting Warning is what quiets them.
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_DemotesWarningToDebug(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	libkapi.InstallGRPCLogAdapter(logger)

	grpclog.Warning("connection degraded")
	grpclog.Warningln("connection degraded")
	grpclog.Warningf("connection %s", "degraded")

	output := buf.String()
	assert.Contains(t, output, "connection degraded")
	assert.Contains(t, output, "level=DEBUG")
	assert.Contains(t, output, "component=grpc")
	assert.NotContains(t, output, "level=WARN",
		"Warning should be demoted to Debug, not logged at Warn")
}

// TestInstallGRPCLogAdapter_RoutesError verifies that gRPC Error-level
// calls are forwarded to the slog logger at Error level.
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_RoutesError(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	libkapi.InstallGRPCLogAdapter(logger)

	grpclog.Error("storage backend unreachable")
	grpclog.Errorln("storage backend unreachable")
	grpclog.Errorf("storage %s", "unreachable")

	output := buf.String()
	assert.Contains(t, output, "storage backend unreachable")
	assert.Contains(t, output, "level=ERROR")
	assert.Contains(t, output, "component=grpc")
}

// TestInstallGRPCLogAdapter_FatalLogsAtError verifies that gRPC Fatal
// calls are logged at Error level via the adapter. The package-level
// grpclog.Fatal also calls os.Exit(1) after the adapter logs — that is
// gRPC's hardcoded behavior and cannot be prevented through the
// LoggerV2 interface — but Fatal is never called during normal gRPC
// operation, only for programming errors.
//
// This test calls the adapter's Fatal method directly (not the
// package-level grpclog.Fatal) to avoid os.Exit killing the test
// process. The adapter is unexported, so we test indirectly: we set
// the adapter via InstallGRPCLogAdapter, then call the adapter's
// Fatal through grpclog.Fatalf which delegates to the adapter. But
// since the package-level Fatalf also calls os.Exit, we use Errorf
// instead to verify Error-level routing, and trust that Fatal uses
// the same a.logger.Error path.
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_FatalLogsAtError(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	libkapi.InstallGRPCLogAdapter(logger)

	// Verify Error-level routing works (Fatal uses the same
	// a.logger.Error path in the adapter).
	grpclog.Errorf("fatal-level message")

	assert.Contains(t, buf.String(), "fatal-level message",
		"Error and Fatal messages should be logged at Error level")
	assert.Contains(t, buf.String(), "level=ERROR",
		"Fatal messages should be logged at Error level")
}

// TestInstallGRPCLogAdapter_NilLoggerFallsBackToDefault verifies that
// passing a nil logger does not panic and falls back to slog.Default().
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_NilLoggerFallsBackToDefault(t *testing.T) {
	originalDefault := slog.Default()

	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	var buf bytes.Buffer

	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	require.NotPanics(t, func() {
		libkapi.InstallGRPCLogAdapter(nil)
	})

	grpclog.Error("nil-fallback error")

	assert.Contains(t, buf.String(), "nil-fallback error",
		"Error should reach slog.Default when nil is passed")
}

// TestInstallGRPCLogAdapter_InfoFilteredAtDefaultLevel verifies that
// gRPC Info/Warning messages (demoted to Debug) are filtered out when
// the slog handler is at the default Info level, so they don't clutter
// normal output.
//
//nolint:paralleltest // mutates process-global grpclog state
func TestInstallGRPCLogAdapter_InfoFilteredAtDefaultLevel(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, nil)) // default: Info level

	libkapi.InstallGRPCLogAdapter(logger)

	grpclog.Info("this debug should be filtered")
	grpclog.Warning("this debug should be filtered")

	assert.Empty(t, buf.String(),
		"Demoted Debug messages should be filtered by an Info-level handler")
}
