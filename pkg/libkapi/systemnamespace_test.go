package libkapi //nolint:testpackage // exercises unexported internals: config, resolvedSystemNamespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConfig_ResolvedSystemNamespace_DefaultsToLibkapi verifies that
// resolvedSystemNamespace falls back to "libkapi" when WithSystemNamespace
// is never applied.
func TestConfig_ResolvedSystemNamespace_DefaultsToLibkapi(t *testing.T) {
	t.Parallel()

	cfg := config{}

	assert.Equal(t, defaultSystemNamespace, cfg.resolvedSystemNamespace())
}

// TestConfig_ResolvedSystemNamespace_UsesConfigured verifies that
// resolvedSystemNamespace respects a namespace set via WithSystemNamespace.
func TestConfig_ResolvedSystemNamespace_UsesConfigured(t *testing.T) {
	t.Parallel()

	cfg := config{systemNamespace: "custom-ns"}

	assert.Equal(t, "custom-ns", cfg.resolvedSystemNamespace())
}
