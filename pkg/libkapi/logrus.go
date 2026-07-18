package libkapi

import (
	"io"
	"log/slog"
	"slices"

	"github.com/sirupsen/logrus"
)

// InstallLogrusAdapter bridges logrus output to the consumer's slog
// logger so that libraries using logrus (e.g., kine) route their log
// output through logger instead of logrus's default stderr writer.
//
// After this call, every logrus.Info, logrus.Warn, logrus.Error, and
// logrus.Fatal call on the standard logger is forwarded to the slog
// handler backing logger. logrus's own stderr output is suppressed
// (SetOutput(io.Discard)) and logrus.Fatalf no longer calls os.Exit
// (ExitFunc set to a no-op), so a logrus.Fatalf — e.g., from kine's
// MustCommit/MustRollback during a compaction failure — logs at Error
// and returns instead of killing the process.
//
// Level mapping:
//   - Trace/Debug → slog.Debug
//   - Info        → slog.Info
//   - Warn        → slog.Warn
//   - Error/Fatal/Panic → slog.Error
//
// Like InstallKlogAdapter and InstallGRPCLogAdapter, this installs a
// process-wide global: logrus's standard logger is a singleton, so the
// last call wins. New calls this before storage.Resolve and buildServer
// so that all logrus output from kine and its drivers is captured.
//
// If logger is nil, slog.Default() is used, matching Config.Logger's
// fallback behavior.
func InstallLogrusAdapter(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	stdLogger := logrus.StandardLogger()

	// Replace all hooks so repeated calls don't accumulate duplicates.
	stdLogger.ReplaceHooks(logrus.LevelHooks{})

	stdLogger.AddHook(&logrusHook{logger: logger.With("component", "kine")})

	// Suppress logrus's own output: the hook already routes to slog.
	stdLogger.SetOutput(io.Discard)

	// Set level to Trace so hooks fire for all levels, not just the
	// default Info+.
	stdLogger.SetLevel(logrus.TraceLevel)

	// Prevent logrus.Fatalf from calling os.Exit — kine's
	// MustCommit/MustRollback (pkg/drivers/generic/tx.go:41,53) call
	// logrus.Fatalf when a compaction transaction fails. Without this
	// override, a momentary DB outage during a compaction window kills
	// the process. With it, the error is logged and the compactor
	// retries on the next interval.
	stdLogger.ExitFunc = func(int) {}
}

// logrusHook implements logrus.Hook by routing each entry to the
// consumer's slog logger with the correct level mapping.
type logrusHook struct {
	logger *slog.Logger
}

// Levels returns all logrus levels so the hook fires for every entry.
func (h *logrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire routes the logrus entry to slog, mapping logrus levels to slog
// levels. Error, Fatal, and Panic all map to slog.Error: Fatal and
// Panic are log-level-wise errors even if they also trigger exit/panic
// side effects.
func (h *logrusHook) Fire(entry *logrus.Entry) error {
	args := entryToSlogArgs(entry.Data)

	switch entry.Level {
	case logrus.TraceLevel, logrus.DebugLevel:
		h.logger.Debug(entry.Message, args...)
	case logrus.InfoLevel:
		h.logger.Info(entry.Message, args...)
	case logrus.WarnLevel:
		h.logger.Warn(entry.Message, args...)
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		h.logger.Error(entry.Message, args...)
	}

	return nil
}

// keyAndValue is the number of args per logrus field (key + value).
const keyAndValue = 2

// entryToSlogArgs converts logrus Fields (map[string]interface{}) to
// slog key-value pairs. Sorted by key for deterministic output.
func entryToSlogArgs(fields logrus.Fields) []any {
	if len(fields) == 0 {
		return nil
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	args := make([]any, 0, len(keys)*keyAndValue)
	for _, k := range keys {
		args = append(args, k, fields[k])
	}

	return args
}
