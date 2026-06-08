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
	envBaseURL                            = "KOMMODITY_BASE_URL"
	envServerPort                         = "KOMMODITY_PORT"
	envAdminGroup                         = "KOMMODITY_ADMIN_GROUP"
	envDisableAuth                        = "KOMMODITY_INSECURE_DISABLE_AUTHENTICATION"
	envOIDCIssuerURL                      = "KOMMODITY_OIDC_ISSUER_URL"
	envOIDCClientID                       = "KOMMODITY_OIDC_CLIENT_ID"
	envOIDCUsernameClaim                  = "KOMMODITY_OIDC_USERNAME_CLAIM"
	envOIDCGroupsClaim                    = "KOMMODITY_OIDC_GROUPS_CLAIM"
	envDatabaseURI                        = "KOMMODITY_DB_URI"
	envAttestationNonceTTL                = "KOMMODITY_ATTESTATION_NONCE_TTL"
	envDevelopmentMode                    = "KOMMODITY_DEVELOPMENT_MODE"
	envKineURI                            = "KOMMODITY_KINE_URI"
	envInfrastructureProviders            = "KOMMODITY_INFRASTRUCTURE_PROVIDERS"
	envAuditPolicyFilePath                = "KOMMODITY_AUDIT_POLICY_FILE_PATH"
	envGarbageCollectorEnabled            = "KOMMODITY_GARBAGE_COLLECTOR_ENABLED"
	envGarbageCollectorWorkers            = "KOMMODITY_GARBAGE_COLLECTOR_WORKERS"
	envGarbageCollectorSyncPeriod         = "KOMMODITY_GARBAGE_COLLECTOR_SYNC_PERIOD"
	envGarbageCollectorInitialSyncTimeout = "KOMMODITY_GARBAGE_COLLECTOR_INITIAL_SYNC_TIMEOUT"
	envTalosProxyEnabled                  = "KOMMODITY_TALOS_PROXY_ENABLED"
	envTalosProxyPort                     = "KOMMODITY_TALOS_PROXY_PORT"
	envTalosProxyNamespace                = "KOMMODITY_TALOS_PROXY_NAMESPACE"
	envTalosProxyServiceName              = "KOMMODITY_TALOS_PROXY_SERVICE_NAME"
	envTalosProxyIdleTimeout              = "KOMMODITY_TALOS_PROXY_IDLE_TIMEOUT"
	envAzureEmbeddedARMReconciler         = "KOMMODITY_AZURE_EMBEDDED_ARM_RECONCILER"
	//nolint:gosec // G101: env var name, not a credential
	envAzureDefaultCredentialSecret = "KOMMODITY_AZURE_DEFAULT_CREDENTIAL_SECRET"

	defaultServerPort                         = 5000
	defaultAPIServerPort                      = 8443
	defaultDisableAuth                        = false
	defaultOIDCUsernameClaim                  = "email"
	defaultOIDCGroupsClaim                    = "groups"
	defaultDevelopmentMode                    = false
	defaultKineURI                            = "unix://bin/kine.sock"
	defaultAttestationNonceTTL                = 5 * time.Minute
	defaultTalosProxyEnabled                  = true
	defaultTalosProxyPort                     = 15050
	defaultTalosProxyNamespace                = "talos-cluster-proxy"
	defaultTalosProxyServiceName              = "talos-cluster-proxy"
	defaultTalosProxyIdleTimeout              = 1 * time.Minute
	defaultGarbageCollectorEnabled            = true
	defaultGarbageCollectorWorkers            = 5
	defaultGarbageCollectorSyncPeriod         = 30 * time.Second
	defaultGarbageCollectorInitialSyncTimeout = 60 * time.Second
	defaultAzureEmbeddedARMReconciler         = true
	defaultAzureDefaultCredentialSecret       = ""
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
	TalosProxyConfig        *TalosProxyConfig
	GarbageCollectorConfig  *GarbageCollectorConfig
	AuditPolicyFilePath     string
	DevelopmentMode         bool
	InfrastructureProviders []Provider
	AzureConfig             *AzureConfig
}

// AzureConfig holds configuration for the embedded Azure integration.
type AzureConfig struct {
	// EmbeddedARMReconcilerEnabled enables the in-process Azure ARM reconciler
	// that materializes ASO custom resources directly into Azure, replacing the
	// external Azure Service Operator sidecar.
	EmbeddedARMReconcilerEnabled bool
	// DefaultCredentialSecret is the fallback Secret name used to resolve Azure
	// credentials when a custom resource carries no
	// "serviceoperator.azure.com/credential-from" annotation.
	DefaultCredentialSecret string
}

// GarbageCollectorConfig holds the configuration for the embedded
// ownerReferences-based garbage collector.
type GarbageCollectorConfig struct {
	Enabled            bool
	Workers            int
	SyncPeriod         time.Duration
	InitialSyncTimeout time.Duration
}

// TalosProxyConfig holds the configuration for the transparent Talos gRPC proxy.
type TalosProxyConfig struct {
	Enabled          bool
	ListenPort       int
	ProxyNamespace   string
	ProxyServiceName string
	IdleTimeout      time.Duration
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
	infrastructureProviders := getInfrastructureProviders(ctx)

	adminGroup, err := getAdminGroup()
	if apply && err != nil {
		return nil, fmt.Errorf("failed to get admin group: %w", err)
	}

	dbURI, err := getDatabaseURI()
	if err != nil {
		return nil, fmt.Errorf("failed to get database URI: %w", err)
	}

	talosProxyConfig := getTalosProxyConfig(ctx)
	garbageCollectorConfig := getGarbageCollectorConfig(ctx)
	azureConfig := getAzureConfig(ctx)

	return &KommodityConfig{
		BaseURL:             baseURL,
		ServerPort:          serverPort,
		APIServerPort:       defaultAPIServerPort,
		WebhookPort:         ctrlwebhook.DefaultPort,
		DBURI:               dbURI,
		KineURI:             kineURI,
		AttestationConfig:   getAttestationConfig(ctx),
		AuditPolicyFilePath: getAuditPolicyFilePath(ctx),
		AuthConfig: &AuthConfig{
			Apply:      apply,
			OIDCConfig: oidcConfig,
			AdminGroup: adminGroup,
		},
		ClientConfig:            &ClientConfig{},
		TalosProxyConfig:        talosProxyConfig,
		GarbageCollectorConfig:  garbageCollectorConfig,
		DevelopmentMode:         developmentMode,
		InfrastructureProviders: infrastructureProviders,
		AzureConfig:             azureConfig,
	}, nil
}

func getAzureConfig(ctx context.Context) *AzureConfig {
	return &AzureConfig{
		EmbeddedARMReconcilerEnabled: getAzureEmbeddedARMReconcilerEnabled(ctx),
		DefaultCredentialSecret:      getAzureDefaultCredentialSecret(ctx),
	}
}

func getAzureEmbeddedARMReconcilerEnabled(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	enabled := os.Getenv(envAzureEmbeddedARMReconciler)
	if enabled == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envAzureEmbeddedARMReconciler),
			zap.Bool("default", defaultAzureEmbeddedARMReconciler))

		return defaultAzureEmbeddedARMReconciler
	}

	enabledBool, err := strconv.ParseBool(enabled)
	if err != nil {
		logger.Info("failed to convert azure embedded ARM reconciler flag to boolean",
			zap.String("envVar", envAzureEmbeddedARMReconciler),
			zap.String("value", enabled),
			zap.Bool("default", defaultAzureEmbeddedARMReconciler))

		return defaultAzureEmbeddedARMReconciler
	}

	return enabledBool
}

func getAzureDefaultCredentialSecret(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	secret := os.Getenv(envAzureDefaultCredentialSecret)
	if secret == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envAzureDefaultCredentialSecret),
			zap.String("default", defaultAzureDefaultCredentialSecret))

		return defaultAzureDefaultCredentialSecret
	}

	return secret
}

func getBaseURL(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	baseURL := os.Getenv(envBaseURL)
	if baseURL == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envBaseURL),
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

func getInfrastructureProviders(ctx context.Context) []Provider {
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
		for p := range strings.SplitSeq(providersEnv, ",") {
			provider := Provider(strings.TrimSpace(p))
			providers = append(providers, provider)
		}
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

func getAuditPolicyFilePath(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	policyFilePath := os.Getenv(envAuditPolicyFilePath)
	if policyFilePath == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envAuditPolicyFilePath),
			zap.String("default", ""))

		return ""
	}

	return policyFilePath
}

func getTalosProxyConfig(ctx context.Context) *TalosProxyConfig {
	return &TalosProxyConfig{
		Enabled:          getTalosProxyEnabled(ctx),
		ListenPort:       getTalosProxyPort(ctx),
		ProxyNamespace:   getTalosProxyNamespace(ctx),
		ProxyServiceName: getTalosProxyServiceName(ctx),
		IdleTimeout:      getTalosProxyIdleTimeout(ctx),
	}
}

func getTalosProxyEnabled(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	enabled := os.Getenv(envTalosProxyEnabled)
	if enabled == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envTalosProxyEnabled),
			zap.Bool("default", defaultTalosProxyEnabled))

		return defaultTalosProxyEnabled
	}

	enabledBool, err := strconv.ParseBool(enabled)
	if err != nil {
		logger.Info("failed to convert talos proxy enabled to boolean",
			zap.String("envVar", envTalosProxyEnabled),
			zap.String("value", enabled),
			zap.Bool("default", defaultTalosProxyEnabled))

		return defaultTalosProxyEnabled
	}

	return enabledBool
}

func getTalosProxyPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	port := os.Getenv(envTalosProxyPort)
	if port == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envTalosProxyPort),
			zap.Int("default", defaultTalosProxyPort))

		return defaultTalosProxyPort
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		logger.Info("failed to convert talos proxy port to integer",
			zap.String("envVar", envTalosProxyPort),
			zap.String("value", port),
			zap.Int("default", defaultTalosProxyPort))

		return defaultTalosProxyPort
	}

	return portInt
}

func getTalosProxyNamespace(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	namespace := os.Getenv(envTalosProxyNamespace)
	if namespace == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envTalosProxyNamespace),
			zap.String("default", defaultTalosProxyNamespace))

		return defaultTalosProxyNamespace
	}

	return namespace
}

func getTalosProxyServiceName(ctx context.Context) string {
	logger := logging.FromContext(ctx)

	name := os.Getenv(envTalosProxyServiceName)
	if name == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envTalosProxyServiceName),
			zap.String("default", defaultTalosProxyServiceName))

		return defaultTalosProxyServiceName
	}

	return name
}

func getTalosProxyIdleTimeout(ctx context.Context) time.Duration {
	logger := logging.FromContext(ctx)

	idleTimeout := os.Getenv(envTalosProxyIdleTimeout)
	if idleTimeout == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envTalosProxyIdleTimeout),
			zap.String("default", defaultTalosProxyIdleTimeout.String()))

		return defaultTalosProxyIdleTimeout
	}

	duration, err := time.ParseDuration(idleTimeout)
	if err != nil {
		logger.Info("failed to parse talos proxy idle timeout",
			zap.String("envVar", envTalosProxyIdleTimeout),
			zap.String("value", idleTimeout),
			zap.String("default", defaultTalosProxyIdleTimeout.String()))

		return defaultTalosProxyIdleTimeout
	}

	return duration
}

func getGarbageCollectorConfig(ctx context.Context) *GarbageCollectorConfig {
	return &GarbageCollectorConfig{
		Enabled:            getGarbageCollectorEnabled(ctx),
		Workers:            getGarbageCollectorWorkers(ctx),
		SyncPeriod:         getGarbageCollectorSyncPeriod(ctx),
		InitialSyncTimeout: getGarbageCollectorInitialSyncTimeout(ctx),
	}
}

func getGarbageCollectorEnabled(ctx context.Context) bool {
	logger := logging.FromContext(ctx)

	enabled := os.Getenv(envGarbageCollectorEnabled)
	if enabled == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envGarbageCollectorEnabled),
			zap.Bool("default", defaultGarbageCollectorEnabled))

		return defaultGarbageCollectorEnabled
	}

	enabledBool, err := strconv.ParseBool(enabled)
	if err != nil {
		logger.Info("failed to convert garbage collector enabled to boolean",
			zap.String("envVar", envGarbageCollectorEnabled),
			zap.String("value", enabled),
			zap.Bool("default", defaultGarbageCollectorEnabled))

		return defaultGarbageCollectorEnabled
	}

	return enabledBool
}

func getGarbageCollectorWorkers(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	workers := os.Getenv(envGarbageCollectorWorkers)
	if workers == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envGarbageCollectorWorkers),
			zap.Int("default", defaultGarbageCollectorWorkers))

		return defaultGarbageCollectorWorkers
	}

	workersInt, err := strconv.Atoi(workers)
	if err != nil || workersInt < 1 {
		logger.Info("failed to convert garbage collector workers to positive integer",
			zap.String("envVar", envGarbageCollectorWorkers),
			zap.String("value", workers),
			zap.Int("default", defaultGarbageCollectorWorkers))

		return defaultGarbageCollectorWorkers
	}

	return workersInt
}

func getGarbageCollectorSyncPeriod(ctx context.Context) time.Duration {
	logger := logging.FromContext(ctx)

	syncPeriod := os.Getenv(envGarbageCollectorSyncPeriod)
	if syncPeriod == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envGarbageCollectorSyncPeriod),
			zap.String("default", defaultGarbageCollectorSyncPeriod.String()))

		return defaultGarbageCollectorSyncPeriod
	}

	duration, err := time.ParseDuration(syncPeriod)
	if err != nil {
		logger.Info("failed to parse garbage collector sync period",
			zap.String("envVar", envGarbageCollectorSyncPeriod),
			zap.String("value", syncPeriod),
			zap.String("default", defaultGarbageCollectorSyncPeriod.String()))

		return defaultGarbageCollectorSyncPeriod
	}

	return duration
}

func getGarbageCollectorInitialSyncTimeout(ctx context.Context) time.Duration {
	logger := logging.FromContext(ctx)

	timeout := os.Getenv(envGarbageCollectorInitialSyncTimeout)
	if timeout == "" {
		logger.Info(configurationNotSpecified,
			zap.String("envVar", envGarbageCollectorInitialSyncTimeout),
			zap.String("default", defaultGarbageCollectorInitialSyncTimeout.String()))

		return defaultGarbageCollectorInitialSyncTimeout
	}

	duration, err := time.ParseDuration(timeout)
	if err != nil {
		logger.Info("failed to parse garbage collector initial sync timeout",
			zap.String("envVar", envGarbageCollectorInitialSyncTimeout),
			zap.String("value", timeout),
			zap.String("default", defaultGarbageCollectorInitialSyncTimeout.String()))

		return defaultGarbageCollectorInitialSyncTimeout
	}

	return duration
}
