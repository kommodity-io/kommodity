// Package config provides configuration settings for the API server.
package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	restclient "k8s.io/client-go/rest"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

const (
	defaultServerPort          = 5000
	defaultAPIServerPort       = 8443
	defaultDisableAuth         = false
	defaultOIDCUsernameClaim   = "email"
	defaultOIDCGroupsClaim     = "groups"
	defaultDevelopmentMode     = false
	defaultKineURI             = "unix://bin/kine.sock"
	defaultAttestationNonceTTL = 5 * time.Minute

	// EnvBaseURL environment variable for Kommodity base URL.
	EnvBaseURL = "KOMMODITY_BASE_URL"

	envServerPort              = "KOMMODITY_PORT"
	envAdminGroup              = "KOMMODITY_ADMIN_GROUP"
	envDisableAuth             = "KOMMODITY_INSECURE_DISABLE_AUTHENTICATION"
	envOIDCIssuerURL           = "KOMMODITY_OIDC_ISSUER_URL"
	envOIDCClientID            = "KOMMODITY_OIDC_CLIENT_ID"
	envOIDCUsernameClaim       = "KOMMODITY_OIDC_USERNAME_CLAIM"
	envOIDCGroupsClaim         = "KOMMODITY_OIDC_GROUPS_CLAIM"
	envDatabaseURI             = "KOMMODITY_DB_URI"
	envAttestationNonceTTL     = "KOMMODITY_ATTESTATION_NONCE_TTL"
	envDevelopmentMode         = "KOMMODITY_DEVELOPMENT_MODE"
	envKineURI                 = "KOMMODITY_KINE_URI"
	envInfrastructureProviders = "KOMMODITY_INFRASTRUCTURE_PROVIDERS"
)

const (
	configurationNotSpecified = "Configuration not specified, using default value"
)

// KommodityConfig holds the configuration settings for the Kommodity API server.
type KommodityConfig struct {
	BaseURL                 string
	ServerPort              int
	APIServerPort           int
	WebhookPort             int
	DBURI                   *url.URL
	KineURI                 string
	AttestationConfig       *AttestationConfig
	AuthConfig              *AuthConfig
	ClientConfig            *ClientConfig
	DevelopmentMode         bool
	InfrastructureProviders []Provider
}

// AuthConfig holds the authentication configuration settings for the Kommodity API server.
type AuthConfig struct {
	Apply      bool
	OIDCConfig *OIDCConfig
	AdminGroup string
}

// AttestationConfig holds the attestation configuration settings for the Kommodity API server.
type AttestationConfig struct {
	NonceTTL time.Duration
}

// ClientConfig holds the client configuration settings for the Kommodity API server.
type ClientConfig struct {
	LoopbackClientConfig *restclient.Config
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
	baseURL := getBaseURL(ctx)
	serverPort := getServerPort(ctx)
	apply := getApplyAuth(ctx)
	oidcConfig := getOIDCConfig(ctx)
	developmentMode := getDevelopmentMode(ctx)
	kineURI := getKineURI(ctx)
	infrastructureProviders := getInfrastructureProviders(ctx, developmentMode)

	adminGroup, err := getAdminGroup()
	if apply && err != nil {
		return nil, fmt.Errorf("failed to get admin group: %w", err)
	}

	dbURI, err := getDatabaseURI()
	if err != nil {
		return nil, fmt.Errorf("failed to get database URI: %w", err)
	}

	return &KommodityConfig{
		BaseURL:           baseURL,
		ServerPort:        serverPort,
		APIServerPort:     defaultAPIServerPort,
		WebhookPort:       ctrlwebhook.DefaultPort,
		DBURI:             dbURI,
		KineURI:           kineURI,
		AttestationConfig: getAttestationConfig(ctx),
		AuthConfig: &AuthConfig{
			Apply:      apply,
			OIDCConfig: oidcConfig,
			AdminGroup: adminGroup,
		},
		ClientConfig:            &ClientConfig{},
		DevelopmentMode:         developmentMode,
		InfrastructureProviders: infrastructureProviders,
	}, nil
}

func getBaseURL(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	baseURL := os.Getenv(EnvBaseURL)
	if baseURL == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", EnvBaseURL),
			zap.String("default", fmt.Sprintf("http://localhost:%d", defaultServerPort)))

		return fmt.Sprintf("http://localhost:%d", defaultServerPort)
	}

	return baseURL
}

func getServerPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	serverPort := os.Getenv(envServerPort)
	if serverPort == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envServerPort),
			zap.Int("default", defaultServerPort))

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
			zap.String("envVar", envDisableAuth),
			zap.Bool("default", defaultDisableAuth))

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

func getAttestationConfig(ctx context.Context) *AttestationConfig {
	logger := logging.FromContext(ctx)

	nonceTTLStr := os.Getenv(envAttestationNonceTTL)
	if nonceTTLStr == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envAttestationNonceTTL),
			zap.String("default", defaultAttestationNonceTTL.String()))

		return &AttestationConfig{
			NonceTTL: defaultAttestationNonceTTL,
		}
	}

	nonceTTL, err := time.ParseDuration(nonceTTLStr)
	if err != nil {
		logger.Info("failed to parse attestation nonce TTL",
			zap.String("envVar", envAttestationNonceTTL),
			zap.String("value", nonceTTLStr),
			zap.String("default", defaultAttestationNonceTTL.String()))

		return &AttestationConfig{
			NonceTTL: defaultAttestationNonceTTL,
		}
	}

	return &AttestationConfig{
		NonceTTL: nonceTTL,
	}
}

func getDevelopmentMode(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	developmentMode := os.Getenv(envDevelopmentMode)
	if developmentMode == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envDevelopmentMode),
			zap.Bool("default", defaultDevelopmentMode))

		return defaultDevelopmentMode
	}

	developmentModeBool, err := strconv.ParseBool(developmentMode)
	if err != nil {
		logger.Info("failed to convert development mode to boolean",
			zap.String("envVar", envDevelopmentMode),
			zap.String("value", developmentMode),
			zap.Bool("default", defaultDevelopmentMode))

		return defaultDevelopmentMode
	}

	return developmentModeBool
}

func getKineURI(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	kineURI := os.Getenv(envKineURI)
	if kineURI == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envKineURI),
			zap.String("default", defaultKineURI))

		return defaultKineURI
	}

	return kineURI
}

func getInfrastructureProviders(ctx context.Context, developmentMode bool) []Provider {
	logger := logging.FromContext(ctx)

	var providers []Provider

	defaultProviders := GetAllProviders()

	providersEnv := os.Getenv(envInfrastructureProviders)
	if providersEnv == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envInfrastructureProviders),
			zap.Any("default", defaultProviders))

		providers = defaultProviders
	} else {
		providerStrings := strings.Split(providersEnv, ",")
		for _, p := range providerStrings {
			provider := Provider(strings.TrimSpace(p))
			providers = append(providers, provider)
		}
	}

	if !developmentMode {
		// Docker is by default added in generated code, so if its not development mode, remove it from the list
		dockerIndex := slices.Index(providers, ProviderDocker)
		providers = append(providers[:dockerIndex], providers[dockerIndex+1:]...)
	}

	// Ensure core CAPI provider are always included
	if !slices.Contains(providers, ProviderCapi) {
		providers = append(providers, ProviderCapi)
	}

	// Ensure Talos provider is always included
	if !slices.Contains(providers, ProviderTalos) {
		providers = append(providers, ProviderTalos)
	}

	return providers
}
