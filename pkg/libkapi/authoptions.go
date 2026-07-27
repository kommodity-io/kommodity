package libkapi

import (
	"context"

	"k8s.io/apiserver/pkg/authorization/authorizer"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// This file re-exports the auth package's public API at the libkapi level,
// so callers can use libkapi.WithOIDC(...) etc. without importing the auth
// subpackage directly. Matches the storage re-export pattern.
//
// Each function below wraps the underlying auth.Option into an Option,
// appending it to cfg.authOpts for New to hand to auth.Resolve. This is a
// pure wrapping step - auth.WithOIDC et al. already just build a closure
// that performs its work (e.g. OIDC discovery) later, when auth.Resolve
// invokes it, so timing is unchanged.

// Authorizer is re-aliased from k8s.io/apiserver so callers can pass custom
// authorizers to WithAuthorizer without importing the k8s.io package directly.
type Authorizer = authorizer.Authorizer

// OIDCConfig configures an OIDC bearer-token authenticator.
type OIDCConfig = auth.OIDCConfig

// ServiceAccountConfig configures ServiceAccount token authentication and management.
type ServiceAccountConfig = auth.ServiceAccountConfig

// KeyPersistenceConfig configures signing-key persistence to a Kubernetes Secret.
type KeyPersistenceConfig = auth.KeyPersistenceConfig

// AdminAuthorizerConfig configures the admin authorizer.
type AdminAuthorizerConfig = auth.AdminAuthorizerConfig

// ServiceAccountTokenGetter provides access to ServiceAccount, Pod, Secret,
// and Node objects for token validation.
type ServiceAccountTokenGetter = auth.ServiceAccountTokenGetter

// SecretsGetter provides access to Secret objects.
type SecretsGetter = auth.SecretsGetter

// WithOIDC adds an OIDC authenticator to the chain. See auth.WithOIDC.
func WithOIDC(cfg OIDCConfig) Option {
	return func(_ context.Context, c *config) error {
		c.authOpts = append(c.authOpts, auth.WithOIDC(cfg))

		return nil
	}
}

// WithServiceAccount adds a ServiceAccount token authenticator to the chain.
// See auth.WithServiceAccount.
func WithServiceAccount(cfg ServiceAccountConfig) Option {
	return func(_ context.Context, c *config) error {
		c.authOpts = append(c.authOpts, auth.WithServiceAccount(cfg))

		return nil
	}
}

// WithAdminAuthorizer sets the admin authorizer. See auth.WithAdminAuthorizer.
func WithAdminAuthorizer(cfg AdminAuthorizerConfig) Option {
	return func(_ context.Context, c *config) error {
		c.authOpts = append(c.authOpts, auth.WithAdminAuthorizer(cfg))

		return nil
	}
}

// WithAuthorizer sets a custom authorizer. See auth.WithAuthorizer.
func WithAuthorizer(authz Authorizer) Option {
	return func(_ context.Context, c *config) error {
		c.authOpts = append(c.authOpts, auth.WithAuthorizer(authz))

		return nil
	}
}
