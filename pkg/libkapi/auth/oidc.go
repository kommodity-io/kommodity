package auth

import (
	"context"
	"fmt"

	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	oidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
)

// OIDCConfig configures an OIDC bearer-token authenticator.
type OIDCConfig struct {
	// IssuerURL is the OIDC issuer URL (e.g. "https://accounts.google.com").
	// The server fetches the issuer's discovery document at {IssuerURL}/.well-known/openid-configuration.
	IssuerURL string

	// ClientID is the OAuth 2.0 client ID. Tokens must be issued for this audience.
	ClientID string

	// UsernameClaim is the JWT claim to use as the username.
	// Default: "email".
	UsernameClaim string

	// GroupsClaim is the JWT claim to use for group membership.
	// Default: "groups".
	GroupsClaim string

	// ExtraScopes are additional OAuth 2.0 scopes requested during token validation.
	ExtraScopes []string

	// SigningAlgs are the accepted JWT signing algorithms.
	// Default: ["RS256"].
	SigningAlgs []string
}

// WithOIDC adds an OIDC authenticator to the chain. The OIDC provider's
// discovery document is fetched immediately from IssuerURL during option
// application, so the context's timeout/cancellation applies.
//
// Mirrors pkg/server/auth.go's OIDC wiring (lines 182-221).
func WithOIDC(oidcCfg OIDCConfig) Option {
	return func(ctx context.Context, cfg *config) error {
		if oidcCfg.IssuerURL == "" {
			return ErrOIDCIssuerRequired
		}

		if oidcCfg.ClientID == "" {
			return ErrOIDCClientIDRequired
		}

		jwtAuthenticator := BuildJWTAuthenticator(oidcCfg)

		signingAlgs := oidcCfg.SigningAlgs
		if len(signingAlgs) == 0 {
			signingAlgs = []string{"RS256"}
		}

		oidcAuth, err := oidc.New(ctx, oidc.Options{
			JWTAuthenticator:     jwtAuthenticator,
			SupportedSigningAlgs: signingAlgs,
		})
		if err != nil {
			return fmt.Errorf("failed to setup oidc authenticator: %w", err)
		}

		cfg.oidcAuthenticator = bearertoken.New(oidcAuth)
		cfg.apiAudiences = authenticator.Audiences(jwtAuthenticator.Issuer.Audiences)

		return nil
	}
}

// BuildJWTAuthenticator constructs the JWT authenticator from the OIDC config,
// applying defaults for UsernameClaim ("email") and GroupsClaim ("groups").
func BuildJWTAuthenticator(oidcCfg OIDCConfig) apiserver.JWTAuthenticator {
	usernameClaim := oidcCfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "email"
	}

	groupsClaim := oidcCfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	prefix := ""

	return apiserver.JWTAuthenticator{
		Issuer: apiserver.Issuer{
			URL:       oidcCfg.IssuerURL,
			Audiences: []string{oidcCfg.ClientID},
		},
		ClaimMappings: apiserver.ClaimMappings{
			Username: apiserver.PrefixedClaimOrExpression{
				Claim:  usernameClaim,
				Prefix: &prefix,
			},
			Groups: apiserver.PrefixedClaimOrExpression{
				Claim:  groupsClaim,
				Prefix: &prefix,
			},
		},
		ClaimValidationRules: []apiserver.ClaimValidationRule{
			{
				Claim:         "aud",
				RequiredValue: oidcCfg.ClientID,
			},
		},
	}
}
