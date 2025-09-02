// Package config provides configuration settings for the API server.
package config

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/logging"
)

const (
	defaultServerPort    = 8080
	defaultAPIServerPort = 8443
)

// GetServerPort retrieves the server port from the environment variable otherwise uses the default.
func GetServerPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	serverPort := os.Getenv("KOMMODITY_PORT")
	if serverPort == "" {
		logger.Info(fmt.Sprintf("KOMMODITY_PORT is not set, defaulting to %d", defaultServerPort))

		return defaultServerPort
	}

	serverPortInt, err := strconv.Atoi(serverPort)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to convert KOMMODITY_PORT to integer: %v, defaulting to %d",
			serverPort, defaultServerPort))

		return defaultServerPort
	}

	return serverPortInt
}

// GetAPIServerPort retrieves the API server port.
func GetAPIServerPort(_ context.Context) int {
	return defaultAPIServerPort
}
