// Package main provides an example of how to use the logging package.
package main

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
)

func main() {
	logger := logging.NewLogger()

	port := 8080

	// Don't do this.
	logger.Info(fmt.Sprintf("Starting HTTP server on port %d", port))
	logger.Sugar().Infof("Starting HTTP server on port %d", port)

	// Do this instead.
	logger.Info("Starting HTTP server", zap.Int("port", port))

	printLog(logger)

	// This is how you can use a context to pass a logger around.
	ctx := logging.WithLogger(context.Background(), logger)
	printLogWithContext(ctx)
}

// This is how you can pass the logger around.
func printLog(logger *zap.Logger) {
	logger.Info("This is a log message", zap.String("key", "value"))
}

// This is how you can use a context to pass the logger around.
func printLogWithContext(ctx context.Context) {
	logger := logging.FromContext(ctx).With(zap.String("context", "example"))
	logger.Info("This is a log message", zap.String("key", "value"))
}
