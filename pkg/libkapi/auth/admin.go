package auth

import (
	"context"
	"slices"
	"strings"

	auth "k8s.io/apiserver/pkg/authorization/authorizer"
)

const (
	systemPrivilegedGroup      = "system:masters"
	systemServiceAccountsGroup = "system:serviceaccounts"
)

// HealthPaths returns endpoints that must be accessible without authentication
// to support liveness, readiness, and health probes.
func HealthPaths() []string {
	return []string{"/livez", "/readyz", "/healthz"}
}

// AdminAuthorizerConfig configures the admin authorizer.
type AdminAuthorizerConfig struct {
	// AdminGroup is the group allowed full access (e.g. "corti-admin").
	// Required.
	AdminGroup string
}

// WithAdminAuthorizer sets an authorizer that allows health check endpoints
// (anonymous), system:masters, the configured AdminGroup, and
// system:serviceaccounts; denies everything else.
//
// Mirrors pkg/server/auth.go's adminAuthorizer (lines 107-152).
func WithAdminAuthorizer(cfg AdminAuthorizerConfig) Option {
	return func(_ context.Context, o *config) error {
		if cfg.AdminGroup == "" {
			return ErrAdminGroupRequired
		}

		o.authorizer = &AdminAuthorizer{Cfg: cfg}

		return nil
	}
}

// AdminAuthorizer allows health endpoints, system:masters, the configured
// admin group, and system:serviceaccounts; denies everything else.
type AdminAuthorizer struct {
	Cfg AdminAuthorizerConfig
}

// Authorize decides whether to allow the request based on health path,
// group membership, or service account identity.
func (a AdminAuthorizer) Authorize(_ context.Context, attrs auth.Attributes) (auth.Decision, string, error) {
	// Allow health check endpoints for all users (including anonymous)
	path := attrs.GetPath()

	for _, healthPath := range HealthPaths() {
		if !attrs.IsResourceRequest() && (path == healthPath || strings.HasPrefix(path, healthPath+"/")) {
			return auth.DecisionAllow, "allowed: health check endpoint", nil
		}
	}

	user := attrs.GetUser()
	if user == nil {
		return auth.DecisionDeny, "no user in attributes", nil
	}

	if slices.Contains(user.GetGroups(), systemPrivilegedGroup) {
		return auth.DecisionAllow, "allowed: user is in system:masters group", nil
	}

	if slices.Contains(user.GetGroups(), a.Cfg.AdminGroup) {
		return auth.DecisionAllow, "allowed: user is in admin group", nil
	}

	// Allow authenticated ServiceAccounts (e.g. cluster autoscaler)
	if slices.Contains(user.GetGroups(), systemServiceAccountsGroup) {
		return auth.DecisionAllow, "allowed: user is an authenticated service account", nil
	}

	return auth.DecisionDeny, "forbidden: user is not in admin group, system:masters group, or a service account", nil
}
