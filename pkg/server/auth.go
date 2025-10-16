package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/storage/selfsubjectaccessreviews"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	authunion "k8s.io/apiserver/pkg/authentication/request/union"
	requnion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authentication/user"
	auth "k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	genericapiserver "k8s.io/apiserver/pkg/server"
	oidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
)

const (
	systemPrivilegedGroup = "system:masters"
)

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

	return auth.DecisionDeny, "forbidden: user is not in admin group or system:masters group", nil
}

// NewSelfSubjectAccessReviewREST creates a new REST storage for SelfSubjectAccessReview.
func NewSelfSubjectAccessReviewREST(cfg *config.KommodityConfig) *selfsubjectaccessreviews.SelfSubjectAccessReviewREST {
	return &selfsubjectaccessreviews.SelfSubjectAccessReviewREST{
		Authorizer: adminAuthorizer{cfg: cfg},
	}
}

func applyAuth(ctx context.Context, cfg *config.KommodityConfig, config *genericapiserver.RecommendedConfig) error {
	if !cfg.AuthConfig.Apply {
		config.Authentication.Authenticator = requnion.New(anonymousReqAuth{})
		config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

		return nil
	}

	prefix := ""

	oidcConfig := cfg.AuthConfig.OIDCConfig
	if oidcConfig == nil {
		return ErrMissingOIDCConfig
	}

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

	config.Authorization.Authorizer = &adminAuthorizer{}

	config.Authentication.APIAudiences = authenticator.Audiences(jwtAuthenticator.Issuer.Audiences)
	config.Authentication.Authenticator = authunion.New(bearerOIDC, anonymousReqAuth{})

	return nil
}
