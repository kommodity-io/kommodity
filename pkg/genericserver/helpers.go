package genericserver

import (
	"context"
	"os"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
)

// DefaultPort is the default port for the server.
const DefaultPort = 8080

// getPort returns the port to listen on. It checks the
// PORT environment variable first, then defaults to 8080.
func getPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	rawPort := os.Getenv("PORT")
	if rawPort == "" {
		return DefaultPort
	}

	port, err := strconv.Atoi(rawPort)
	if err != nil {
		logger.Error("Failed to parse port", zap.Error(err), zap.String("rawPort", rawPort))
		logger.Warn("Falling back to default port", zap.Int("port", DefaultPort))

		return DefaultPort
	}

	return port
}
