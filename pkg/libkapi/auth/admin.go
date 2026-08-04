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
	// AdminGroups is a comma-delimited list of groups allowed full access
	// (e.g. "corti-admin" or "corti-admin,corti-sre"). Surrounding
	// whitespace around each group is trimmed. At least one group is
	// required.
	AdminGroups string
}

// WithAdminAuthorizer sets an authorizer that allows health check endpoints
// (anonymous), system:masters, the configured AdminGroups, and
// system:serviceaccounts; denies everything else.
//
// Mirrors pkg/server/auth.go's adminAuthorizer (lines 107-152).
//
// Clears any RBACListerSource a preceding WithRBACAuthorizer installed:
// options are applied in order with the last authorizer-setting call
// winning (see WithAuthorizer's doc), and a stale RBACListerSource left in
// place would still make buildServer's finishRBACAuthorizer wire up and
// start RBAC informers for listers nothing reads anymore — wasted watches
// and API load for no effect on the actual, overwritten authorizer.
func WithAdminAuthorizer(adminCfg AdminAuthorizerConfig) Option {
	return func(_ context.Context, cfg *config) error {
		groups := parseAdminGroups(adminCfg.AdminGroups)
		if len(groups) == 0 {
			return ErrAdminGroupRequired
		}

		cfg.authorizer = &AdminAuthorizer{Groups: groups}
		cfg.rbacListerSource = nil

		return nil
	}
}

// parseAdminGroups splits a comma-delimited list of group names, trimming
// whitespace and discarding empty entries.
func parseAdminGroups(raw string) []string {
	var groups []string

	for group := range strings.SplitSeq(raw, ",") {
		group = strings.TrimSpace(group)
		if group != "" {
			groups = append(groups, group)
		}
	}

	return groups
}

// AdminAuthorizer allows health endpoints, system:masters, any of the
// configured admin groups, and system:serviceaccounts; denies everything
// else.
type AdminAuthorizer struct {
	Groups []string
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

	if containsAny(user.GetGroups(), a.Groups) {
		return auth.DecisionAllow, "allowed: user is in admin group", nil
	}

	// Allow authenticated ServiceAccounts (e.g. cluster autoscaler)
	if slices.Contains(user.GetGroups(), systemServiceAccountsGroup) {
		return auth.DecisionAllow, "allowed: user is an authenticated service account", nil
	}

	return auth.DecisionDeny, "forbidden: user is not in admin group, system:masters group, or a service account", nil
}

// containsAny reports whether groups contains any element of candidates.
func containsAny(groups []string, candidates []string) bool {
	for _, candidate := range candidates {
		if slices.Contains(groups, candidate) {
			return true
		}
	}

	return false
}
