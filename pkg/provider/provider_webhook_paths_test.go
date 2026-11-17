//nolint:testpackage // white-box tests exercise the unexported unservedWebhookPaths set
package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestServedWebhookPathsExcludesUnservedEntries asserts that no path listed as unserved
// (dropped at apply time) leaks into ServedWebhookPaths, which webhook setup cross-checks
// against the registered handlers.
func TestServedWebhookPathsExcludesUnservedEntries(t *testing.T) {
	t.Parallel()

	paths, err := ServedWebhookPaths()
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		assert.NotContains(t, unservedWebhookPaths, path,
			"unserved path %s must not leak into ServedWebhookPaths", path)
	}
}

// TestServedWebhookPathsIncludesAzureClusterIdentity pins the path the bug report is about:
// its entry must stay servable (the handler is registered in pkg/controller/webhook), not
// silently dropped — otherwise identity validation would quietly disappear again.
func TestServedWebhookPathsIncludesAzureClusterIdentity(t *testing.T) {
	t.Parallel()

	paths, err := ServedWebhookPaths()
	require.NoError(t, err)

	assert.Contains(t, paths, "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureclusteridentity")
}

// TestUnservedWebhookPathsAreEmbedded pins every unserved path to an embedded manifest:
// a path that no longer appears in any embedded entry is stale and should be removed from
// the set, and ServedWebhookPaths is the cross-check that keeps the two in sync.
func TestUnservedWebhookPathsAreEmbedded(t *testing.T) {
	t.Parallel()

	allPaths := map[string]struct{}{}

	objs, err := readEmbeddedWebhookConfigurations()
	require.NoError(t, err)

	for _, obj := range objs {
		webhooks, found, err := unstructured.NestedSlice(obj.Object, "webhooks")
		require.NoError(t, err)
		require.True(t, found)

		for _, entry := range webhooks {
			entryMap, ok := entry.(map[string]any)
			require.True(t, ok)

			path, found, err := unstructured.NestedString(entryMap, "clientConfig", "service", "path")
			require.NoError(t, err)
			require.True(t, found)

			allPaths[path] = struct{}{}
		}
	}

	for path := range unservedWebhookPaths {
		assert.Contains(t, allPaths, path, "unserved path %s no longer exists in any embedded manifest", path)
	}
}
