// Package logging provides a simple logger using Uber's zap library,
// that can be configured at runtime using environment variables.
package logging

import (
	"context"

	"go.uber.org/zap"
)

// contextKey key is a key used to store the logger in the context.
type contextKey struct{}

// GetContextKey returns the context key.
func GetContextKey() any {
	return contextKey{}
}

// WithLogger adds a logger to the context.
func WithLogger(ctx context.Context, logger *zap.Logger) context.Context {
	return context.WithValue(ctx, GetContextKey(), logger)
}

// FromContext returns the logger from the context.
func FromContext(ctx context.Context) *zap.Logger {
	if logger, ok := ctx.Value(GetContextKey()).(*zap.Logger); ok {
		return logger
	}

	// Return a new logger if none is found.
	return NewLogger()
}
