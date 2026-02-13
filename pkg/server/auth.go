package server

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"slices"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/storage/selfsubjectaccessreviews"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	authunion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authentication/user"
	auth "k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	genericapiserver "k8s.io/apiserver/pkg/server"
	oidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

const (
	systemPrivilegedGroup      = "system:masters"
	systemServiceAccountsGroup = "system:serviceaccounts"
)

// serviceAccountTokenGetter implements serviceaccount.ServiceAccountTokenGetter
// to validate that ServiceAccounts and Secrets exist in Kommodity.
type serviceAccountTokenGetter struct {
	client kubernetes.Interface
}

func newServiceAccountTokenGetter(config *restclient.Config) (*serviceAccountTokenGetter, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &serviceAccountTokenGetter{client: client}, nil
}

func (g *serviceAccountTokenGetter) GetServiceAccount(namespace, name string) (*corev1.ServiceAccount, error) {
	serviceAccount, err := g.client.CoreV1().
		ServiceAccounts(namespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get service account %s/%s: %w", namespace, name, err)
	}

	return serviceAccount, nil
}

func (g *serviceAccountTokenGetter) GetPod(_, _ string) (*corev1.Pod, error) {
	// Pods are not stored in Kommodity, return not found
	return nil, fmt.Errorf("%w: pods", ErrNotSupportedInKommodity)
}

func (g *serviceAccountTokenGetter) GetSecret(namespace, name string) (*corev1.Secret, error) {
	secret, err := g.client.CoreV1().
		Secrets(namespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}

	return secret, nil
}

func (g *serviceAccountTokenGetter) GetNode(_ string) (*corev1.Node, error) {
	// Nodes are not stored in Kommodity, return not found
	return nil, fmt.Errorf("%w: node", ErrNotSupportedInKommodity)
}

type secretsGetter struct {
	client kubernetes.Interface
}

func (s *secretsGetter) Secrets(namespace string) corev1client.SecretInterface {
	return s.client.CoreV1().Secrets(namespace)
}

type anonymousReqAuth struct{}

func (anonymousReqAuth) AuthenticateRequest(_ *http.Request) (*authenticator.Response, bool, error) {
	return &authenticator.Response{
		User: &user.DefaultInfo{
			Name:   user.Anonymous,                    // "system:anonymous"
			Groups: []string{user.AllUnauthenticated}, // "system:unauthenticated"
		},
	}, true, nil
}

type adminAuthorizer struct {
	cfg *config.KommodityConfig
}

func (a adminAuthorizer) Authorize(_ context.Context, attrs auth.Attributes) (auth.Decision, string, error) {
	if !a.cfg.AuthConfig.Apply {
		return auth.DecisionAllow, "allowed: auth is disabled", nil
	}

	user := attrs.GetUser()
	if user == nil {
		// no user — probably unauthenticated
		return auth.DecisionDeny, "no user in attributes", nil
	}

	adminGroup := a.cfg.AuthConfig.AdminGroup
	if adminGroup == "" {
		return auth.DecisionDeny,
			"forbidden: no admin group configured",
			ErrNoAdminGroupConfigured
	}

	if slices.Contains(user.GetGroups(), systemPrivilegedGroup) {
		return auth.DecisionAllow, "allowed: user is in system:masters group", nil
	}

	if slices.Contains(user.GetGroups(), adminGroup) {
		return auth.DecisionAllow, "allowed: user is in admin group", nil
	}

	// Allow authenticated ServiceAccounts (e.g., cluster autoscaler)
	if slices.Contains(user.GetGroups(), systemServiceAccountsGroup) {
		return auth.DecisionAllow, "allowed: user is an authenticated service account", nil
	}

	return auth.DecisionDeny, "forbidden: user is not in admin group, system:masters group, or a service account", nil
}

// NewSelfSubjectAccessReviewREST creates a new REST storage for SelfSubjectAccessReview.
func NewSelfSubjectAccessReviewREST(cfg *config.KommodityConfig) *selfsubjectaccessreviews.SelfSubjectAccessReviewREST {
	return &selfsubjectaccessreviews.SelfSubjectAccessReviewREST{
		Authorizer: adminAuthorizer{cfg: cfg},
	}
}

//nolint:funlen
func applyAuth(ctx context.Context, cfg *config.KommodityConfig,
	config *genericapiserver.RecommendedConfig, signingKey *rsa.PrivateKey) error {
	if !cfg.AuthConfig.Apply {
		config.Authentication.Authenticator = authunion.New(anonymousReqAuth{})
		config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

		return nil
	}

	// Set up ServiceAccount token authenticator using the in-memory signing key
	saAuthenticator, err := setupServiceAccountAuth(config, signingKey)
	if err != nil {
		return fmt.Errorf("failed to setup serviceaccount authenticator: %w", err)
	}

	bearerSA := bearertoken.New(saAuthenticator)

	// Build list of authenticators - ServiceAccount first, then OIDC if configured
	authenticators := []authenticator.Request{bearerSA}

	oidcConfig := cfg.AuthConfig.OIDCConfig
	if oidcConfig != nil {
		prefix := ""

		jwtAuthenticator := apiserver.JWTAuthenticator{
			Issuer: apiserver.Issuer{
				URL:       oidcConfig.IssuerURL,
				Audiences: []string{oidcConfig.ClientID},
			},
			ClaimMappings: apiserver.ClaimMappings{
				Username: apiserver.PrefixedClaimOrExpression{
					Claim:  oidcConfig.UsernameClaim,
					Prefix: &prefix,
				},
				Groups: apiserver.PrefixedClaimOrExpression{
					Claim:  oidcConfig.GroupsClaim,
					Prefix: &prefix,
				},
			},
			ClaimValidationRules: []apiserver.ClaimValidationRule{
				{
					Claim:         "aud",
					RequiredValue: oidcConfig.ClientID,
				},
			},
		}

		oidcAuth, err := oidc.New(ctx, oidc.Options{
			JWTAuthenticator:     jwtAuthenticator,
			SupportedSigningAlgs: []string{"RS256"},
		})
		if err != nil {
			return fmt.Errorf("failed to setup oidc authenticator: %w", err)
		}

		bearerOIDC := bearertoken.New(oidcAuth)
		authenticators = append(authenticators, bearerOIDC)

		config.Authentication.APIAudiences = authenticator.Audiences(jwtAuthenticator.Issuer.Audiences)
	}

	// Always add anonymous authenticator as fallback
	authenticators = append(authenticators, anonymousReqAuth{})

	config.Authorization.Authorizer = &adminAuthorizer{
		cfg: cfg,
	}

	config.Authentication.Authenticator = authunion.New(authenticators...)

	return nil
}

// setupServiceAccountAuth creates a ServiceAccount token authenticator using the provided signing key.
// It validates that the ServiceAccount and Secret referenced in the token actually exist in Kommodity.
// The signing key is generated in-memory and persisted to a Secret by a PostStartHook.
func setupServiceAccountAuth(
	config *genericapiserver.RecommendedConfig, signingKey *rsa.PrivateKey,
) (authenticator.Token, error) {
	// Create a static public keys getter for the authenticator
	keysGetter, err := serviceaccount.StaticPublicKeysGetter([]any{&signingKey.PublicKey})
	if err != nil {
		return nil, fmt.Errorf("failed to create static public keys getter: %w", err)
	}

	// Create a ServiceAccount token getter to validate SA and Secret exist in Kommodity
	saGetter, err := newServiceAccountTokenGetter(config.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create service account token getter: %w", err)
	}

	// Create a secrets getter for the validator
	secretsGet := &secretsGetter{client: saGetter.client}

	// Create a legacy validator WITH ServiceAccount/Secret lookup enabled
	// This validates that:
	// 1. The ServiceAccount exists in Kommodity
	// 2. The Secret referenced in the token exists
	// 3. The Secret is associated with the ServiceAccount
	validator, err := serviceaccount.NewLegacyValidator(true, saGetter, secretsGet)
	if err != nil {
		return nil, fmt.Errorf("failed to create legacy validator: %w", err)
	}

	// Create the ServiceAccount token authenticator
	// serviceaccount.LegacyIssuer is "kubernetes/serviceaccount"
	saAuth := serviceaccount.JWTTokenAuthenticator(
		[]string{serviceaccount.LegacyIssuer},
		keysGetter,
		nil, // no audience validation for legacy tokens
		validator,
	)

	return saAuth, nil
}
