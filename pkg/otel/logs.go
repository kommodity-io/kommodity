// Package otel provides OpenTelemetry instrumentation and helper function
// to initialize the OpenTelemetry SDK and configure the different signal
// exporters.
package otel

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	otelzap "github.com/kommodity-io/kommodity/pkg/otel/zap"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
)

// Could we use this instead:
// https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/exporters/autoexport/logs.go

type loggerConfig struct {
	// Exporters is a list of exporters to use.
	Exporters []string
	// OTLPEndpoint is the endpoint to use for the OTLP exporter.
	OTLPEndpoint string
}

// newLoggerConfig creates a new logger configuration.
func newLoggerConfig() *loggerConfig {
	rawExporterNames := os.Getenv("OTLP_LOGS_EXPORTER")
	if rawExporterNames == "" {
		rawExporterNames = "console"
	}

	exporterNames := strings.Split(rawExporterNames, ",")

	// Detect if an OTLP endpoint is specified and
	// add the otlp exporter to the list of exporters.
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}

	if otlpEndpoint != "" {
		exporterNames = append(exporterNames, "otlp")
	}

	return &loggerConfig{
		Exporters:    exporterNames,
		OTLPEndpoint: otlpEndpoint,
	}
}

// NewLoggerProvider defaults to a console exporter if no exporter is specified.
// If an OTLP endpoint is specified, it will use that endpoint in addition to the
// console exporter. This is useful to allow developers to still inspect container
// logs in the console while also sending them to a remote endpoint.
func NewLoggerProvider(ctx context.Context) *log.LoggerProvider {
	config := newLoggerConfig()

	providerOptions := make([]log.LoggerProviderOption, 0, len(config.Exporters))

	for _, exporterName := range config.Exporters {
		switch strings.ToLower(exporterName) {
		case "console":
			providerOptions = append(providerOptions, log.WithProcessor(log.NewBatchProcessor(otelzap.New())))
		case "otlp":
			otelURL, err := url.Parse(config.OTLPEndpoint)
			if err != nil {
				fatal(ctx, "failed to parse OTLP endpoint", err)
			}

			var exporter log.Exporter
			if otelURL.Scheme == "http" || otelURL.Scheme == "https" {
				exporter, err = otlploghttp.New(ctx, otlploghttp.WithEndpoint(config.OTLPEndpoint))
				if err != nil {
					fatal(ctx, "failed to create OTLP HTTP exporter", err)
				}
			} else {
				exporter, err = otlploggrpc.New(ctx, otlploggrpc.WithEndpoint(config.OTLPEndpoint))
				if err != nil {
					fatal(ctx, "failed to create OTLP gRPC exporter", err)
				}
			}

			providerOptions = append(providerOptions, log.WithProcessor(log.NewBatchProcessor(exporter)))
		default:
			continue
		}
	}

	return log.NewLoggerProvider(
		providerOptions...,
	)
}

// fatal logs a message and should cancel the context. It is intended
// to be used if the logger configuration fails. It formats the message
// as a JSON object and writes it to stdout.
func fatal(_ context.Context, msg string, fatalError error) {
	err := json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
		"level": "fatal",
		"msg":   msg,
		"error": fatalError,
	})
	if err != nil {
		panic(err)
	}

	os.Exit(1)
}
