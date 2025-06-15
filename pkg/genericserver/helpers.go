package genericserver

import (
	"context"
	"os"
	"strconv"

	"go.uber.org/zap"
)

// DefaultPort is the default port for the server.
const DefaultPort = 8080

// getPort returns the port to listen on. It checks the
// PORT environment variable first, then defaults to 8080.
func getPort(_ context.Context) int {
	logger := zap.L()

	port := os.Getenv("PORT")
	if port == "" {
		return DefaultPort
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		logger.Warn("failed to parse port", zap.Error(err))
		logger.Warn("using default port", zap.Int("port", DefaultPort))

		return DefaultPort
	}

	return portInt
}
