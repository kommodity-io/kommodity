package zap

import (
	"context"

	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var _ sdklog.Exporter = (*Exporter)(nil)

// Option is a function that configures the zap logger.
type Option func(*Exporter)

// WithLogger sets the zap logger to use.
func WithLogger(logger *zap.Logger) Option {
	return func(e *Exporter) {
		e.logger = logger
	}
}

// Exporter implements log.Exporter to print
// human-readable log records to a zap logger.
type Exporter struct {
	logger *zap.Logger
}

// New creates a new ZapLogExporter with the provided zap logger.
func New(opts ...Option) *Exporter {
	exporter := &Exporter{}

	for _, opt := range opts {
		opt(exporter)
	}

	if exporter.logger == nil {
		config := zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		config.EncoderConfig.EncodeLevel = zapcore.LowercaseColorLevelEncoder

		// Create a new logger with the specified configuration
		logger, err := config.Build()
		if err != nil {
			// We don't ever expect this to fail.
			panic(err)
		}

		exporter.logger = logger
	}

	return exporter
}

// exportOne prints a single log record.
func (e *Exporter) exportOne(_ context.Context, rec sdklog.Record) error {
	// Convert the log record to a zap field
	fields := make([]zapcore.Field, 0, rec.AttributesLen())
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		fields = append(fields, zap.Any(kv.Key, kv.Value))

		return true
	})

	logFunc := map[log.Severity]func(string, ...zapcore.Field){
		// TODO: Should this be "info" instead of "panic"?
		log.SeverityUndefined: e.logger.Panic,
		log.SeverityTrace1:    e.logger.Debug,
		log.SeverityTrace2:    e.logger.Debug,
		log.SeverityTrace3:    e.logger.Debug,
		log.SeverityTrace4:    e.logger.Debug,
		log.SeverityDebug1:    e.logger.Debug,
		log.SeverityDebug2:    e.logger.Debug,
		log.SeverityDebug3:    e.logger.Debug,
		log.SeverityDebug4:    e.logger.Debug,
		log.SeverityInfo1:     e.logger.Info,
		log.SeverityInfo2:     e.logger.Info,
		log.SeverityInfo3:     e.logger.Info,
		log.SeverityInfo4:     e.logger.Info,
		log.SeverityWarn1:     e.logger.Warn,
		log.SeverityWarn2:     e.logger.Warn,
		log.SeverityWarn3:     e.logger.Warn,
		log.SeverityWarn4:     e.logger.Warn,
		log.SeverityError1:    e.logger.Error,
		log.SeverityError2:    e.logger.Error,
		log.SeverityError3:    e.logger.Error,
		log.SeverityError4:    e.logger.Error,
		log.SeverityFatal1:    e.logger.Fatal,
		log.SeverityFatal2:    e.logger.Fatal,
		log.SeverityFatal3:    e.logger.Fatal,
		log.SeverityFatal4:    e.logger.Fatal,
	}

	// Try to match the severity to a zap log level
	// and default to Info if not found.
	log, ok := logFunc[rec.Severity()]
	if !ok {
		log = e.logger.Info
	}

	log(rec.Body().String(), fields...)

	return nil
}

// Export exports a batch of log records.
func (e *Exporter) Export(ctx context.Context, records []sdklog.Record) error {
	for _, rec := range records {
		if err := e.exportOne(ctx, rec); err != nil {
			return err
		}
	}

	return nil
}

// ForceFlush forces the exporter to flush any buffered log records.
func (e *Exporter) ForceFlush(ctx context.Context) error {
	if err := e.logger.Sync(); err != nil {
		return err
	}

	return nil
}

// Shutdown flushes any buffered log records and closes the logger.
func (e *Exporter) Shutdown(ctx context.Context) error {
	if err := e.logger.Sync(); err != nil {
		return err
	}

	return nil
}
