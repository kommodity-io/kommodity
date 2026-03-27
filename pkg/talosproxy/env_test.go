package talosproxy_test

import (
	"os"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSetProxyEnv_SetsVariables(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	logger := zap.NewNop()

	err := talosproxy.SetProxyEnv(logger, "127.0.0.1:12345")
	require.NoError(t, err)

	assert.Equal(t, "http://127.0.0.1:12345", os.Getenv("HTTPS_PROXY"))
	assert.Contains(t, os.Getenv("NO_PROXY"), "localhost")
	assert.Contains(t, os.Getenv("NO_PROXY"), "127.0.0.0/8")
}

func TestSetProxyEnv_DoesNotOverrideExisting(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://corporate-proxy:8080")
	t.Setenv("NO_PROXY", "")

	logger := zap.NewNop()

	err := talosproxy.SetProxyEnv(logger, "127.0.0.1:12345")
	require.NoError(t, err)

	assert.Equal(t, "http://corporate-proxy:8080", os.Getenv("HTTPS_PROXY"))
}

func TestSetProxyEnv_AppendsToExistingNoProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "internal.corp.com")

	logger := zap.NewNop()

	err := talosproxy.SetProxyEnv(logger, "127.0.0.1:12345")
	require.NoError(t, err)

	noProxy := os.Getenv("NO_PROXY")
	assert.Contains(t, noProxy, "internal.corp.com")
	assert.Contains(t, noProxy, "localhost")
	assert.Contains(t, noProxy, "127.0.0.0/8")
}

func TestSetProxyEnv_NoDuplicateNoProxyEntries(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "localhost,127.0.0.0/8")

	logger := zap.NewNop()

	err := talosproxy.SetProxyEnv(logger, "127.0.0.1:12345")
	require.NoError(t, err)

	// Should not duplicate existing entries
	assert.Equal(t, "localhost,127.0.0.0/8", os.Getenv("NO_PROXY"))
}
