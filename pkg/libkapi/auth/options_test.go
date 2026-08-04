package auth_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestResolve_NoOptions_ReturnsDefaults verifies that calling Resolve with
// no options produces nil OIDC/SA authenticators and an always-allow authorizer.
func TestResolve_NoOptions_ReturnsDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, nil, slog.Default())
	require.NoError(t, err)

	assert.Nil(t, resolved.OIDCAuthenticator, "expected nil OIDC authenticator")
	assert.Nil(t, resolved.SAConfig, "expected nil SA config")
	assert.Nil(t, resolved.APIAudiences, "expected empty API audiences")

	// Authorizer should be always-allow (the default).
	decision, _, err := resolved.Authorizer.Authorize(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision)
}

// TestResolve_WithAuthorizer_SetsAuthorizer verifies that WithAuthorizer
// sets the authorizer on the resolved config.
func TestResolve_WithAuthorizer_SetsAuthorizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	customAuthz := authorizerfactory.NewAlwaysAllowAuthorizer()

	resolved, err := auth.Resolve(ctx, []auth.Option{auth.WithAuthorizer(customAuthz)}, slog.Default())
	require.NoError(t, err)

	assert.Same(t, customAuthz, resolved.Authorizer)
}

// TestResolve_WithAuthorizer_NilReturnsError verifies that passing a nil
// authorizer to WithAuthorizer returns ErrAuthorizerNil.
func TestResolve_WithAuthorizer_NilReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{auth.WithAuthorizer(nil)}, slog.Default())
	require.ErrorIs(t, err, auth.ErrAuthorizerNil)
}

// TestResolve_WithAuthorizer_AfterRBACAuthorizer_ClearsRBACListerSource
// verifies that a WithAuthorizer call after WithRBACAuthorizer clears
// RBACListerSource, not just Authorizer — otherwise buildServer's
// finishRBACAuthorizer would still wire up and start RBAC informers for
// listers the final (overwritten) authorizer never reads, wasting API
// server watches for no effect.
func TestResolve_WithAuthorizer_AfterRBACAuthorizer_ClearsRBACListerSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	customAuthz := authorizerfactory.NewAlwaysAllowAuthorizer()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: "my-admins"}),
		auth.WithAuthorizer(customAuthz),
	}, slog.Default())
	require.NoError(t, err)

	assert.Same(t, customAuthz, resolved.Authorizer)
	assert.Nil(t, resolved.RBACListerSource)
}

// TestResolve_WithAdminAuthorizer_AfterRBACAuthorizer_ClearsRBACListerSource
// is the same regression guard as
// TestResolve_WithAuthorizer_AfterRBACAuthorizer_ClearsRBACListerSource,
// for WithAdminAuthorizer instead of WithAuthorizer.
func TestResolve_WithAdminAuthorizer_AfterRBACAuthorizer_ClearsRBACListerSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: "my-admins"}),
		auth.WithAdminAuthorizer(auth.AdminAuthorizerConfig{AdminGroups: "my-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	assert.Nil(t, resolved.RBACListerSource)
}

// TestResolve_AuthWithoutAuthorizer_LogsWarning verifies that when
// authentication strategies are configured but no authorizer is set,
// Resolve uses always-allow and the logger receives a warning.
func TestResolve_AuthWithoutAuthorizer_LogsWarning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Capture log output via a test writer.
	var buf strings.Builder

	handler := slog.NewTextHandler(&buf, nil)
	logger := slog.New(handler)

	// Configure SA (which sets saConfig) without an authorizer.
	saOpt := auth.WithServiceAccount(auth.ServiceAccountConfig{})
	_, err := auth.Resolve(ctx, []auth.Option{saOpt}, logger)
	require.NoError(t, err)

	// The warning should have been logged.
	assert.Contains(t, buf.String(), "no authorizer set")
}

// TestBuildUnionAuthenticator_NoAuthenticators_ReturnsAnonymous verifies
// that BuildUnionAuthenticator with no strategies returns an anonymous
// authenticator (not a union).
func TestBuildUnionAuthenticator_NoAuthenticators_ReturnsAnonymous(t *testing.T) {
	t.Parallel()

	authn := auth.BuildUnionAuthenticator(nil, nil)

	// Anonymous authenticator always returns system:anonymous.
	req := newRequestWithNoAuth()

	resp, ok, err := authn.AuthenticateRequest(req)
	require.NoError(t, err)
	assert.True(t, ok)
	require.NotNil(t, resp)
	assert.Equal(t, "system:anonymous", resp.User.GetName())
}

// TestBuildUnionAuthenticator_WithOIDC_IncludesAnonymousFallback verifies
// that BuildUnionAuthenticator with an OIDC authenticator still includes
// anonymous as the fallback.
func TestBuildUnionAuthenticator_WithOIDC_IncludesAnonymousFallback(t *testing.T) {
	t.Parallel()

	// A fake authenticator that always denies (so we fall through to anonymous).
	fakeOIDC := &fakeAuthenticator{ok: false}

	authn := auth.BuildUnionAuthenticator(fakeOIDC, nil)

	req := newRequestWithNoAuth()

	resp, ok, err := authn.AuthenticateRequest(req)
	require.NoError(t, err)
	assert.True(t, ok, "anonymous fallback should authenticate")
	assert.Equal(t, "system:anonymous", resp.User.GetName())
}

// TestBuildUnionAuthenticator_Both_IncludesBoth verifies that both
// authenticators are in the union and the fallback is appended.
func TestBuildUnionAuthenticator_Both_IncludesBoth(t *testing.T) {
	t.Parallel()

	// Both authenticators deny, so anonymous fallback kicks in.
	oidcAuth := &fakeAuthenticator{ok: false}
	saAuth := &fakeAuthenticator{ok: false}

	authn := auth.BuildUnionAuthenticator(oidcAuth, saAuth)

	req := newRequestWithNoAuth()

	resp, ok, err := authn.AuthenticateRequest(req)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "system:anonymous", resp.User.GetName())

	// Both should have been called at least once.
	assert.Positive(t, oidcAuth.callCount, "OIDC authenticator should have been tried")
	assert.Positive(t, saAuth.callCount, "SA authenticator should have been tried")
}

// TestResolve_OptionError_StopsAndReturns verifies that if one option
// returns an error, Resolve stops applying further options and returns
// the error. Uses WithOIDC with an empty IssuerURL to trigger an error.
func TestResolve_OptionError_StopsAndReturns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// WithOIDC with empty IssuerURL returns ErrOIDCIssuerRequired.
	badOIDC := auth.WithOIDC(auth.OIDCConfig{ClientID: "test-client"})

	_, err := auth.Resolve(ctx, []auth.Option{badOIDC}, slog.Default())
	require.ErrorIs(t, err, auth.ErrOIDCIssuerRequired)
}
