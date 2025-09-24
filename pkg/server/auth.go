package server

import (
	"context"
	"fmt"

	kommodityconfig "github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	authunion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	genericapiserver "k8s.io/apiserver/pkg/server"
	oidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
)

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

	config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

	config.Authentication.APIAudiences = authenticator.Audiences(jwtAuthenticator.Issuer.Audiences)
	config.Authentication.Authenticator = authunion.New(bearerOIDC)

	return nil
}
