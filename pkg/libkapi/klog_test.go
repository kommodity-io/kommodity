package libkapi_test

import (
	"bytes"
	"log/slog"
	"testing"

	"k8s.io/klog/v2"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests must NOT use t.Parallel: klog's backing logger is
// process-global and InstallKlogAdapter mutates it. Running in parallel
// with sibling tests (or with TestServerEndToEnd, which calls New and
// therefore InstallKlogAdapter) would race on the global state.
//
// t.Cleanup(klog.ClearLogger) restores the default klog logger after
// each test so the mutation does not leak into later tests.

// TestInstallKlogAdapter_BridgesKlogToSlog verifies that after calling
// InstallKlogAdapter, klog log calls are forwarded to the consumer's slog
// logger instead of klog's default stderr writer.
//
//nolint:paralleltest // mutates process-global klog state
func TestInstallKlogAdapter_BridgesKlogToSlog(t *testing.T) {
	t.Cleanup(klog.ClearLogger)

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	libkapi.InstallKlogAdapter(logger)

	const msg = "klog message routed through slog"
	klog.Info(msg)

	output := buf.String()
	assert.Contains(t, output, msg, "klog.Info output should appear in the slog logger")
	assert.Contains(t, output, "component=apiserver",
		"klog output should be tagged with component=apiserver")
}

// TestInstallKlogAdapter_NilLoggerFallsBackToDefault verifies that passing
// a nil logger does not panic and falls back to slog.Default().
//
//nolint:paralleltest // mutates process-global klog state
func TestInstallKlogAdapter_NilLoggerFallsBackToDefault(t *testing.T) {
	t.Cleanup(klog.ClearLogger)

	var buf bytes.Buffer

	originalDefault := slog.Default()

	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	require.NotPanics(t, func() {
		libkapi.InstallKlogAdapter(nil)
	})

	const msg = "nil-fallback klog message"
	klog.Info(msg)

	assert.Contains(t, buf.String(), msg, "klog.Info should reach slog.Default when nil is passed")
}

// TestInstallKlogAdapter_RespectsSlogLevel verifies that klog calls honor
// the slog handler's level: Info-level klog calls are filtered out when the
// handler is set to Error, while Error-level calls pass through.
//
//nolint:paralleltest // mutates process-global klog state
func TestInstallKlogAdapter_RespectsSlogLevel(t *testing.T) {
	t.Cleanup(klog.ClearLogger)

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	libkapi.InstallKlogAdapter(logger)

	klog.Info("this info should be filtered")
	klog.Error("this error should pass through")

	output := buf.String()
	assert.NotContains(t, output, "this info should be filtered",
		"Info-level klog call should be filtered by the Error-level slog handler")
	assert.Contains(t, output, "this error should pass through",
		"Error-level klog call should reach the slog logger")
}
