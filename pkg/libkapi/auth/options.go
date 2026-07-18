package auth

import (
	"context"
	"log/slog"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/anonymous"
	authunion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
)

// Option configures authentication and authorization for a Server.
// The context is passed so options that require I/O (e.g. OIDC discovery)
// can use it.
type Option func(ctx context.Context, o *config) error

// config holds resolved state from all applied Options. Unexported.
type config struct {
	oidcAuthenticator authenticator.Request
	saConfig          *ServiceAccountConfig
	authorizer        authorizer.Authorizer
	apiAudiences      authenticator.Audiences
}

// ResolvedConfig is the resolved authentication/authorization state, ready
// to be wired into a server by buildServer. The SA authenticator is not
// built here — it needs the server's LoopbackClientConfig — so SAConfig
// is returned for buildServer to call BuildSAAuthenticator.
type ResolvedConfig struct {
	// OIDCAuthenticator is the OIDC bearer-token authenticator, or nil
	// if WithOIDC was not used.
	OIDCAuthenticator authenticator.Request

	// SAConfig is the ServiceAccount configuration, or nil if
	// WithServiceAccount was not used. buildServer calls
	// BuildSAAuthenticator with this and the server's loopback config.
	SAConfig *ServiceAccountConfig

	// Authorizer is the resolved authorizer. Defaults to always-allow
	// if no authorizer option was used.
	Authorizer authorizer.Authorizer

	// APIAudiences is the resolved API audiences for token validation.
	// Empty if none were set.
	APIAudiences authenticator.Audiences
}

// Resolve applies all options in order and returns the resolved configuration.
func Resolve(ctx context.Context, opts []Option, logger *slog.Logger) (*ResolvedConfig, error) {
	cfg := &config{}

	for _, opt := range opts {
		err := opt(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}

	return &ResolvedConfig{
		OIDCAuthenticator: cfg.oidcAuthenticator,
		SAConfig:          cfg.saConfig,
		Authorizer:        resolveAuthorizer(cfg, logger),
		APIAudiences:      cfg.apiAudiences,
	}, nil
}

// resolveAuthorizer returns the configured authorizer, or the always-allow
// default with a warning if authentication strategies were configured but
// no authorizer was set.
func resolveAuthorizer(cfg *config, logger *slog.Logger) authorizer.Authorizer {
	if cfg.authorizer != nil {
		return cfg.authorizer
	}

	if cfg.oidcAuthenticator != nil || cfg.saConfig != nil {
		logger.Warn("Authentication strategies configured but no authorizer set; " +
			"using always-allow authorizer")
	}

	return authorizerfactory.NewAlwaysAllowAuthorizer()
}

// BuildUnionAuthenticator assembles the final union authenticator from the
// individual strategy authenticators. If both are nil, returns anonymous
// (the libkapi default). Otherwise chains them with anonymous as the
// fallback, matching pkg/server/auth.go's authenticator ordering.
func BuildUnionAuthenticator(oidcAuth authenticator.Request, saAuth authenticator.Request) authenticator.Request {
	var authenticators []authenticator.Request

	if oidcAuth != nil {
		authenticators = append(authenticators, oidcAuth)
	}

	if saAuth != nil {
		authenticators = append(authenticators, saAuth)
	}

	if len(authenticators) == 0 {
		return anonymous.NewAuthenticator(nil)
	}

	// Always append anonymous as the fallback so unrecognized tokens
	// resolve to system:anonymous rather than failing.
	authenticators = append(authenticators, anonymous.NewAuthenticator(nil))

	return authunion.New(authenticators...)
}

// WithAuthorizer sets a custom authorizer. If not called, the server uses
// always-allow (with a warning if authentication strategies are configured).
func WithAuthorizer(authz authorizer.Authorizer) Option {
	return func(_ context.Context, cfg *config) error {
		if authz == nil {
			return ErrAuthorizerNil
		}

		cfg.authorizer = authz

		return nil
	}
}
