package api //nolint:testpackage // exercises unexported OIDC extraction internals.

import (
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const structuredAuthConfig = `apiVersion: v1alpha1
kind: KubeAuthenticationConfig
configuration:
  anonymous:
    enabled: true
  jwt:
    - issuer:
        url: https://sso.example.com
        audiences:
          - kommodity
      claimMappings:
        username:
          claim: email
          prefix: "https://sso.example.com#"
        groups:
          claim: groups
          prefix: ""
`

const legacyAuthConfig = `version: v1alpha1
cluster:
  apiServer:
    extraArgs:
      oidc-issuer-url: https://sso.example.com
      oidc-client-id: kommodity
      oidc-username-claim: email
      oidc-groups-claim: groups
`

func TestGetOIDCFromAuthenticationConfig(t *testing.T) {
	t.Parallel()

	provider, err := configloader.NewFromBytes([]byte(structuredAuthConfig))
	require.NoError(t, err)

	oidcConfig := getOIDCFromAuthenticationConfig(provider)
	require.NotNil(t, oidcConfig)
	assert.Equal(t, "https://sso.example.com", oidcConfig.IssuerURL)
	assert.Equal(t, "kommodity", oidcConfig.ClientID)
	assert.Equal(t, "email", oidcConfig.UsernameClaim)
	assert.Equal(t, "groups", oidcConfig.GroupsClaim)
}

func TestGetOIDCFromAuthenticationConfigAbsent(t *testing.T) {
	t.Parallel()

	provider, err := configloader.NewFromBytes([]byte("version: v1alpha1\ncluster: {}\n"))
	require.NoError(t, err)

	assert.Nil(t, getOIDCFromAuthenticationConfig(provider))
}

func TestGetOIDCFromAuthenticationConfigNoJWT(t *testing.T) {
	t.Parallel()

	noJWT := "apiVersion: v1alpha1\nkind: KubeAuthenticationConfig\nconfiguration:\n  jwt: []\n"
	provider, err := configloader.NewFromBytes([]byte(noJWT))
	require.NoError(t, err)

	assert.Nil(t, getOIDCFromAuthenticationConfig(provider))
}

func TestGetOIDCFromExtraArgs(t *testing.T) {
	t.Parallel()

	provider, err := configloader.NewFromBytes([]byte(legacyAuthConfig))
	require.NoError(t, err)

	oidcConfig, err := getOIDCFromExtraArgs(provider)
	require.NoError(t, err)
	assert.Equal(t, "https://sso.example.com", oidcConfig.IssuerURL)
	assert.Equal(t, "kommodity", oidcConfig.ClientID)
	assert.Equal(t, "email", oidcConfig.UsernameClaim)
	assert.Equal(t, "groups", oidcConfig.GroupsClaim)
}

func TestGetOIDCFromExtraArgsNotConfigured(t *testing.T) {
	t.Parallel()

	provider, err := configloader.NewFromBytes([]byte("version: v1alpha1\ncluster: {}\n"))
	require.NoError(t, err)

	_, err = getOIDCFromExtraArgs(provider)
	assert.ErrorIs(t, err, ErrOIDCNotConfigured)
}

func TestStructuredConfigPreferredOverLegacy(t *testing.T) {
	t.Parallel()

	combined := structuredAuthConfig + "---\n" + legacyAuthConfig

	provider, err := configloader.NewFromBytes([]byte(combined))
	require.NoError(t, err)

	oidcConfig := getOIDCFromAuthenticationConfig(provider)
	require.NotNil(t, oidcConfig)
	assert.Equal(t, config.OIDCConfig{
		IssuerURL:     "https://sso.example.com",
		ClientID:      "kommodity",
		UsernameClaim: "email",
		GroupsClaim:   "groups",
	}, *oidcConfig)
}
