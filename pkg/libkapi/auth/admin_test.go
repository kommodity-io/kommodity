package auth_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestWithAdminAuthorizer_MissingAdminGroup_ReturnsError verifies that
// WithAdminAuthorizer returns ErrAdminGroupRequired when AdminGroups is
// empty.
func TestWithAdminAuthorizer_MissingAdminGroup_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: ""}),
	}, slog.Default())

	require.ErrorIs(t, err, auth.ErrAdminGroupRequired)
}

// TestWithAdminAuthorizer_BlankGroupList_ReturnsError verifies that
// WithAdminAuthorizer returns ErrAdminGroupRequired when AdminGroups
// contains only commas and whitespace.
func TestWithAdminAuthorizer_BlankGroupList_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: " , ,"}),
	}, slog.Default())

	require.ErrorIs(t, err, auth.ErrAdminGroupRequired)
}

// TestWithAdminAuthorizer_SetsAuthorizer verifies that WithAdminAuthorizer
// sets a non-nil authorizer on the resolved config.
func TestWithAdminAuthorizer_SetsAuthorizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: "my-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	assert.NotNil(t, resolved.Authorizer, "authorizer should be set")
}

// resolveAdminAuthorizer builds an admin authorizer via the public API
// for use in tests. Returns the authorizer ready to call Authorize on.
func resolveAdminAuthorizer(t *testing.T, adminGroup string) authorizer.Authorizer {
	t.Helper()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: adminGroup}),
	}, slog.Default())
	require.NoError(t, err)

	return resolved.Authorizer
}

// TestAdminAuthorizer_AllowsHealthEndpoints verifies that health check
// paths are allowed for all users (including anonymous).
func TestAdminAuthorizer_AllowsHealthEndpoints(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")

	for _, path := range auth.HealthPaths() {
		attrs := &fakeAttributes{path: path, user: nil}
		decision, _, err := authz.Authorize(context.Background(), attrs)
		require.NoError(t, err)
		assert.Equal(t, authorizer.DecisionAllow, decision, "expected health path %q to be allowed", path)
	}
}

// TestAdminAuthorizer_DeniesNonHealthForAnonymous verifies that non-health
// non-resource paths is denied for anonymous users.
func TestAdminAuthorizer_DeniesNonHealthForAnonymous(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")
	anonUser := &user.DefaultInfo{Name: "system:anonymous", Groups: []string{"system:unauthenticated"}}
	attrs := &fakeAttributes{path: "/api/v1/namespaces", user: anonUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionDeny, decision)
}

// TestAdminAuthorizer_DeniesNilUser verifies that nil user is denied
// (except for health endpoints).
func TestAdminAuthorizer_DeniesNilUser(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")
	attrs := &fakeAttributes{path: "/api/v1/pods", user: nil}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionDeny, decision)
}

// TestAdminAuthorizer_AllowsSystemMasters verifies that system:masters
// group is always allowed.
func TestAdminAuthorizer_AllowsSystemMasters(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")
	adminUser := &user.DefaultInfo{Name: "admin", Groups: []string{"system:masters"}}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: adminUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}

// TestAdminAuthorizer_AllowsAdminGroup verifies that the configured admin
// group is allowed.
func TestAdminAuthorizer_AllowsAdminGroup(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "corti-admin")
	adminUser := &user.DefaultInfo{Name: "user@example.com", Groups: []string{"corti-admin"}}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: adminUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}

// TestAdminAuthorizer_AllowsAnyConfiguredGroup verifies that a comma-
// delimited AdminGroups list allows a user in any one of the listed
// groups, tolerating surrounding whitespace around each entry.
func TestAdminAuthorizer_AllowsAnyConfiguredGroup(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "corti-admin, corti-sre ,corti-oncall")
	adminUser := &user.DefaultInfo{Name: "user@example.com", Groups: []string{"corti-sre"}}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: adminUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}

// TestAdminAuthorizer_AllowsServiceAccounts verifies that
// system:serviceaccounts group is allowed.
func TestAdminAuthorizer_AllowsServiceAccounts(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")
	saUser := &user.DefaultInfo{
		Name:   "system:serviceaccount:default:my-sa",
		Groups: []string{"system:serviceaccounts"},
	}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: saUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}

// TestAdminAuthorizer_DeniesUnknownGroup verifies that users in no known
// group are denied.
func TestAdminAuthorizer_DeniesUnknownGroup(t *testing.T) {
	t.Parallel()

	authz := resolveAdminAuthorizer(t, "my-admins")
	unknownUser := &user.DefaultInfo{Name: "user@example.com", Groups: []string{"some-other-group"}}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: unknownUser}

	decision, _, err := authz.Authorize(context.Background(), attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionDeny, decision)
}

// TestHealthPaths_ReturnsExpected verifies that HealthPaths returns the
// expected set of endpoints.
func TestHealthPaths_ReturnsExpected(t *testing.T) {
	t.Parallel()

	paths := auth.HealthPaths()
	assert.Contains(t, paths, "/healthz")
	assert.Contains(t, paths, "/livez")
	assert.Contains(t, paths, "/readyz")
	assert.Len(t, paths, 3)
}

// TestAdminAuthorizer_OverridesDefault verifies that WithAdminAuthorizer
// overrides the default always-allow authorizer.
func TestAdminAuthorizer_OverridesDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: "my-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	// The resolved authorizer should NOT be always-allow (which would allow
	// everything). Verify by checking that an unknown user is denied.
	unknownUser := &user.DefaultInfo{Name: "unknown", Groups: []string{"unknown-group"}}
	attrs := &fakeAttributes{path: "/api/v1/pods", user: unknownUser}

	decision, _, err := resolved.Authorizer.Authorize(ctx, attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionDeny, decision)

	// Sanity check: always-allow would have allowed this.
	alwaysAllow := authorizerfactory.NewAlwaysAllowAuthorizer()
	decision, _, _ = alwaysAllow.Authorize(ctx, attrs)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}
