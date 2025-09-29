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
	defaultServerPort        = 8080
	defaultAPIServerPort     = 8443
	defaultDisableAuth       = false
	defaultOIDCUsernameClaim = "email"
	defaultOIDCGroupsClaim   = "groups"

	envServerPort        = "KOMMODITY_PORT"
	envAdminGroup        = "KOMMODITY_ADMIN_GROUP"
	envDisableAuth       = "KOMMODITY_INSECURE_DISABLE_AUTHENTICATION"
	envOIDCIssuerURL     = "KOMMODITY_OIDC_ISSUER_URL"
	envOIDCClientID      = "KOMMODITY_OIDC_CLIENT_ID"
	envOIDCUsernameClaim = "KOMMODITY_OIDC_USERNAME_CLAIM"
	envOIDCGroupsClaim   = "KOMMODITY_OIDC_GROUPS_CLAIM"
)

// OIDCConfig holds the OIDC configuration settings from the environment variables.
type OIDCConfig struct {
	IssuerURL     string
	ClientID      string
	UsernameClaim string
	GroupsClaim   string
	ExtraScopes   []string
}

// GetServerPort retrieves the server port from the environment variable otherwise uses the default.
func GetServerPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	serverPort := os.Getenv(envServerPort)
	if serverPort == "" {
		logger.Info(fmt.Sprintf("%s is not set, defaulting to %d", envServerPort, defaultServerPort))

		return defaultServerPort
	}

	serverPortInt, err := strconv.Atoi(serverPort)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to convert %s to integer: %v, defaulting to %d",
			envServerPort, serverPort, defaultServerPort))

		return defaultServerPort
	}

	return serverPortInt
}

// GetAPIServerPort retrieves the API server port.
func GetAPIServerPort(_ context.Context) int {
	return defaultAPIServerPort
}

// ApplyAuth retrieves whether to disable authentication from the environment variable otherwise uses the default.
func ApplyAuth(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	serverPort := os.Getenv(envDisableAuth)
	if serverPort == "" {
		logger.Info(fmt.Sprintf("%s is not set, defaulting to %t", envDisableAuth, defaultDisableAuth))

		return defaultDisableAuth
	}

	disableAuth, err := strconv.ParseBool(serverPort)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to convert %s to boolean: %v, defaulting to %t",
			envDisableAuth, serverPort, defaultDisableAuth))

		return defaultDisableAuth
	}

	return !disableAuth
}

// GetOIDCConfig retrieves the OIDC configuration from the environment variables.
func GetOIDCConfig(ctx context.Context) *OIDCConfig {
	logger := logging.FromContext(ctx)

	issuerURL := os.Getenv(envOIDCIssuerURL)
	clientID := os.Getenv(envOIDCClientID)
	usernameClaim := os.Getenv(envOIDCUsernameClaim)
	groupsClaim := os.Getenv(envOIDCGroupsClaim)

	if usernameClaim == "" {
		usernameClaim = defaultOIDCUsernameClaim
	}

	if groupsClaim == "" {
		groupsClaim = defaultOIDCGroupsClaim
	}

	if issuerURL == "" || clientID == "" {
		logger.Info("No OIDC configuration found in environment variables")

		return nil
	}

	return &OIDCConfig{
		IssuerURL:     issuerURL,
		ClientID:      clientID,
		UsernameClaim: usernameClaim,
		GroupsClaim:   groupsClaim,
	}
}

// GetAdminGroup retrieves the admin group from the environment variable.
func GetAdminGroup() (string, error) {
	adminGroup := os.Getenv(envAdminGroup)
	if adminGroup == "" {
		return "", fmt.Errorf("%w: %s", ErrAdminGroupNotSet, envAdminGroup)
	}

	return adminGroup, nil
}
