package logging

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// FormatJSON indicates the JSON log format.
	FormatJSON string = "json"
	// FormatConsole indicates a human-readable console log format.
	FormatConsole string = "console"
	// DefaultLevel is the default log level if none is specified.
	// We default to WarnLevel to avoid excessive logging in production.
	// This can be overridden by the LOG_LEVEL environment variable.
	DefaultLevel zapcore.Level = zapcore.WarnLevel

	// fatalErrorTemplate is an emergency log message that is returned if the creation of the logger fails.
	fatalErrorTemplate string = `{"level":"error","ts":"%s","msg":"failed to create logger: %s"}`
)

// NewLogger creates a new logger.
func NewLogger() *zap.Logger {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	format, formatInvalid, rawFormat := getFormat()
	if format == FormatConsole {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.LowercaseColorLevelEncoder
	}

	// Common settings for all formats.
	config.OutputPaths = []string{"stdout"}

	level, levelInvalid, rawLevel := getLevel()
	config.Level = level

	logger, err := config.Build()
	if err != nil {
		//nolint:forbidigo // This is required if the logger cannot be created.
		fmt.Printf(fatalErrorTemplate, time.Now().Format(time.RFC3339), err)
		os.Exit(1)
	}

	if formatInvalid {
		logger.Warn("Invalid log format", zap.String("raw_format", rawFormat))
		logger.Warn("Allowed formats: json, console")
		logger.Warn("Using default format", zap.String("format", format))
	}

	if levelInvalid {
		logger.Warn("Invalid log level", zap.String("raw_level", rawLevel))
		logger.Warn("Allowed levels: debug, info, warn, error, fatal")
		logger.Warn("Using default level", zap.String("level", level.String()))
	}

	return logger
}

// getFormat extracts the log format from the environment variable
// and returns it. It also returns a boolean indicating whether
// the extracted format is valid.
func getFormat() (string, bool, string) {
	rawFormat := os.Getenv("LOG_FORMAT")

	if rawFormat != "" {
		if rawFormat != FormatJSON && rawFormat != FormatConsole {
			return FormatJSON, true, rawFormat
		}

		return rawFormat, false, rawFormat
	}

	return FormatJSON, false, rawFormat
}

// getLevel extracts the log level from the environment variable
// and returns it. It also returns a boolean indicating whether
// the level is user-provided and valid.
func getLevel() (zap.AtomicLevel, bool, string) {
	rawLevel := os.Getenv("LOG_LEVEL")

	if rawLevel != "" {
		level, err := zap.ParseAtomicLevel(rawLevel)
		if err != nil {
			return zap.NewAtomicLevelAt(DefaultLevel), true, rawLevel
		}

		return level, false, rawLevel
	}

	return zap.NewAtomicLevelAt(DefaultLevel), false, rawLevel
}
