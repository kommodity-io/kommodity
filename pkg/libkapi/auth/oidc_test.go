package auth_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestWithOIDC_MissingIssuerURL_ReturnsError verifies that WithOIDC
// returns ErrOIDCIssuerRequired when IssuerURL is empty.
func TestWithOIDC_MissingIssuerURL_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithOIDC(auth.OIDCConfig{
			IssuerURL: "",
			ClientID:  "test-client",
		}),
	}, slog.Default())

	require.ErrorIs(t, err, auth.ErrOIDCIssuerRequired)
}

// TestWithOIDC_MissingClientID_ReturnsError verifies that WithOIDC
// returns ErrOIDCClientIDRequired when ClientID is empty.
func TestWithOIDC_MissingClientID_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithOIDC(auth.OIDCConfig{
			IssuerURL: "https://example.com",
			ClientID:  "",
		}),
	}, slog.Default())

	require.ErrorIs(t, err, auth.ErrOIDCClientIDRequired)
}

// TestBuildJWTAuthenticator_DefaultsClaims verifies that BuildJWTAuthenticator
// applies the correct defaults for UsernameClaim and GroupsClaim.
func TestBuildJWTAuthenticator_DefaultsClaims(t *testing.T) {
	t.Parallel()

	jwt := auth.BuildJWTAuthenticator(auth.OIDCConfig{
		IssuerURL: "https://example.com",
		ClientID:  "test-client",
	})

	assert.Equal(t, "https://example.com", jwt.Issuer.URL)
	assert.Equal(t, []string{"test-client"}, jwt.Issuer.Audiences)
	assert.Equal(t, "email", jwt.ClaimMappings.Username.Claim)
	assert.Equal(t, "groups", jwt.ClaimMappings.Groups.Claim)

	// Should have a claim validation rule for "aud" == ClientID.
	require.Len(t, jwt.ClaimValidationRules, 1)
	assert.Equal(t, "aud", jwt.ClaimValidationRules[0].Claim)
	assert.Equal(t, "test-client", jwt.ClaimValidationRules[0].RequiredValue)
}

// TestBuildJWTAuthenticator_CustomClaims verifies that BuildJWTAuthenticator
// respects custom UsernameClaim and GroupsClaim values.
func TestBuildJWTAuthenticator_CustomClaims(t *testing.T) {
	t.Parallel()

	jwt := auth.BuildJWTAuthenticator(auth.OIDCConfig{
		IssuerURL:     "https://example.com",
		ClientID:      "test-client",
		UsernameClaim: "sub",
		GroupsClaim:   "memberOf",
	})

	assert.Equal(t, "sub", jwt.ClaimMappings.Username.Claim)
	assert.Equal(t, "memberOf", jwt.ClaimMappings.Groups.Claim)
}
