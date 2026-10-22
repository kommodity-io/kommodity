package auth_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

const (
	testClientID  = "test-client"
	testIssuerURL = "https://example.com"
)

// TestWithOIDC_MissingIssuerURL_ReturnsError verifies that WithOIDC
// returns ErrOIDCIssuerRequired when IssuerURL is empty.
func TestWithOIDC_MissingIssuerURL_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithOIDC(auth.OIDCConfig{
			IssuerURL: "",
		ClientID:  testClientID,
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
		IssuerURL: testIssuerURL,
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
		IssuerURL: testIssuerURL,
		ClientID:  testClientID,
	})

	assert.Equal(t, testIssuerURL, jwt.Issuer.URL)
	assert.Equal(t, []string{testClientID}, jwt.Issuer.Audiences)
	assert.Equal(t, "email", jwt.ClaimMappings.Username.Claim)
	assert.Equal(t, "groups", jwt.ClaimMappings.Groups.Claim)

	// Should have a claim validation rule for "aud" == ClientID.
	require.Len(t, jwt.ClaimValidationRules, 1)
	assert.Equal(t, "aud", jwt.ClaimValidationRules[0].Claim)
	assert.Equal(t, testClientID, jwt.ClaimValidationRules[0].RequiredValue)
}

// TestBuildJWTAuthenticator_CustomClaims verifies that BuildJWTAuthenticator
// respects custom UsernameClaim and GroupsClaim values.
func TestBuildJWTAuthenticator_CustomClaims(t *testing.T) {
	t.Parallel()

	jwt := auth.BuildJWTAuthenticator(auth.OIDCConfig{
		IssuerURL:     testIssuerURL,
		ClientID:      testClientID,
		UsernameClaim: "sub",
		GroupsClaim:   "memberOf",
	})

	assert.Equal(t, "sub", jwt.ClaimMappings.Username.Claim)
	assert.Equal(t, "memberOf", jwt.ClaimMappings.Groups.Claim)
}
