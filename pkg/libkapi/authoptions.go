package libkapi

import (
	"k8s.io/apiserver/pkg/authorization/authorizer"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// This file re-exports the auth package's public API at the libkapi level,
// so callers can use libkapi.WithOIDC(...) etc. without importing the auth
// subpackage directly. Matches the storage re-export pattern.

// Option is re-aliased from pkg/libkapi/auth. It configures authentication
// and authorization for a Server.
type Option = auth.Option

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
func WithOIDC(cfg OIDCConfig) Option { return auth.WithOIDC(cfg) }

// WithServiceAccount adds a ServiceAccount token authenticator to the chain.
// See auth.WithServiceAccount.
func WithServiceAccount(cfg ServiceAccountConfig) Option { return auth.WithServiceAccount(cfg) }

// WithAdminAuthorizer sets the admin authorizer. See auth.WithAdminAuthorizer.
func WithAdminAuthorizer(cfg AdminAuthorizerConfig) Option { return auth.WithAdminAuthorizer(cfg) }

// WithAuthorizer sets a custom authorizer. See auth.WithAuthorizer.
func WithAuthorizer(authz Authorizer) Option { return auth.WithAuthorizer(authz) }
