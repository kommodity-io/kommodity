package server

import (
	"context"
	"fmt"
	"slices"

	kommodityconfig "github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	authunion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	auth "k8s.io/apiserver/pkg/authorization/authorizer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	oidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
)

const (
	systemPrivilegedGroup = "system:masters"
)

type adminAuthorizer struct{}

func (a *adminAuthorizer) Authorize(_ context.Context, attrs auth.Attributes) (auth.Decision, string, error) {
	user := attrs.GetUser()
	if user == nil {
		// no user — probably unauthenticated
		return authorizer.DecisionDeny, "no user in attributes", nil
	}

	adminGroup, err := kommodityconfig.GetAdminGroup()
	if err != nil {
		return auth.DecisionDeny,
			"forbidden: no admin group configured",
			fmt.Errorf("no admin group configured: %w", err)
	}

	if slices.Contains(user.GetGroups(), systemPrivilegedGroup) {
		return auth.DecisionAllow, "allowed: user is in system:masters group", nil
	}

	if slices.Contains(user.GetGroups(), adminGroup) {
		return auth.DecisionAllow, "allowed: user is in admin group", nil
	}

	return auth.DecisionDeny, "forbidden: user is not in admin group or system:masters group", nil
}

func applyAuth(ctx context.Context, config *genericapiserver.RecommendedConfig) error {
	if !kommodityconfig.ApplyAuth(ctx) {
		return nil
	}

	prefix := ""

	oidcConfig := kommodityconfig.GetOIDCConfig(ctx)
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
	config.Authentication.Authenticator = authunion.New(bearerOIDC)

	return nil
}
