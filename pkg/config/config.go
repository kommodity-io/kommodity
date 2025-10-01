// Package config provides configuration settings for the API server.
package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
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
	envDatabaseURI       = "KOMMODITY_DB_URI"
	envKineURI           = "KOMMODITY_KINE_URI"
)

const (
	configurationNotSpecified = "Configuration not specified, using default value"
)

// KommodityConfig holds the configuration settings for the Kommodity API server.
type KommodityConfig struct {
	ServerPort    int
	APIServerPort int
	DBURI         *url.URL
	KineURI       *string
	AuthConfig    *AuthConfig
}

// AuthConfig holds the authentication configuration settings for the Kommodity API server.
type AuthConfig struct {
	Apply      bool
	OIDCConfig *OIDCConfig
	AdminGroup string
}

// OIDCConfig holds the OIDC configuration settings from the environment variables.
type OIDCConfig struct {
	IssuerURL     string
	ClientID      string
	UsernameClaim string
	GroupsClaim   string
	ExtraScopes   []string
}

// LoadConfig loads the configuration settings from environment variables and returns a KommodityConfig instance.
func LoadConfig(ctx context.Context) (*KommodityConfig, error) {
	serverPort := getServerPort(ctx)
	apply := getApplyAuth(ctx)
	oidcConfig := getOIDCConfig(ctx)

	adminGroup, err := getAdminGroup()
	if apply && err != nil {
		return nil, fmt.Errorf("failed to get admin group: %w", err)
	}

	dbURI, err := getDatabaseURI()
	if err != nil {
		return nil, fmt.Errorf("failed to get database URI: %w", err)
	}

	kineURI, err := getKineURI()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kine URI: %w", err)
	}

	return &KommodityConfig{
		ServerPort:    serverPort,
		APIServerPort: defaultAPIServerPort,
		DBURI:         dbURI,
		KineURI:       kineURI,
		AuthConfig: &AuthConfig{
			Apply:      apply,
			OIDCConfig: oidcConfig,
			AdminGroup: adminGroup,
		},
	}, nil
}

func getServerPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	serverPort := os.Getenv(envServerPort)
	if serverPort == "" {
		logger.Info(configurationNotSpecified,
			zap.Int("default", defaultServerPort),
			zap.String("envVar", envServerPort))

		return defaultServerPort
	}

	serverPortInt, err := strconv.Atoi(serverPort)
	if err != nil {
		logger.Info("failed to convert server port to integer",
			zap.String("envVar", envServerPort),
			zap.String("value", serverPort),
			zap.Int("default", defaultServerPort))

		return defaultServerPort
	}

	return serverPortInt
}

func getApplyAuth(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	disableAuth := os.Getenv(envDisableAuth)
	if disableAuth == "" {
		logger.Info(configurationNotSpecified,
			zap.Bool("default", defaultDisableAuth),
			zap.String("envVar", envDisableAuth))

		return defaultDisableAuth
	}

	disableAuthBool, err := strconv.ParseBool(disableAuth)
	if err != nil {
		logger.Info("failed to convert disable auth to boolean",
			zap.String("envVar", envDisableAuth),
			zap.String("value", disableAuth),
			zap.Bool("default", defaultDisableAuth))

		return defaultDisableAuth
	}

	return !disableAuthBool
}

func getOIDCConfig(ctx context.Context) *OIDCConfig {
	logger := logging.FromContext(ctx)

	issuerURL := os.Getenv(envOIDCIssuerURL)
	clientID := os.Getenv(envOIDCClientID)
	usernameClaim := os.Getenv(envOIDCUsernameClaim)
	groupsClaim := os.Getenv(envOIDCGroupsClaim)

	if usernameClaim == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envOIDCUsernameClaim),
			zap.String("default", defaultOIDCUsernameClaim))

		usernameClaim = defaultOIDCUsernameClaim
	}

	if groupsClaim == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envOIDCGroupsClaim),
			zap.String("default", defaultOIDCGroupsClaim))

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

func getAdminGroup() (string, error) {
	adminGroup := os.Getenv(envAdminGroup)
	if adminGroup == "" {
		return "", fmt.Errorf("%w: %s", ErrAdminGroupNotSet, envAdminGroup)
	}

	return adminGroup, nil
}

func getDatabaseURI() (*url.URL, error) {
	dbURI := os.Getenv(envDatabaseURI)
	if dbURI == "" {
		return nil, ErrKommodityDBEnvVarNotSet
	}

	uri, err := url.Parse(dbURI)
	if err != nil {
		return nil, fmt.Errorf("invalid KOMMODITY_DB_URI: %w", err)
	}

	return uri, nil
}

func getKineURI() (*string, error) {
	kineURI := os.Getenv(envKineURI)
	if kineURI == "" {
		return nil, ErrKommodityKineEnvVarNotSet
	}

	return &kineURI, nil
}
