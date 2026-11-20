//nolint:testpackage // white-box tests exercise the unexported unservedWebhookPaths set
package provider

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const (
	testMutatingGVRResource   = "mutatingwebhookconfigurations"
	testValidatingGVRResource = "validatingwebhookconfigurations"

	testAzureClusterValidatePath         = "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azurecluster"
	testAzureClusterIdentityValidatePath = "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureclusteridentity"
	testAzureManagedClusterValidatePath  = "/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcluster"
	testExtensionConfigValidatePath      = "/validate-runtime-cluster-x-k8s-io-v1alpha1-extensionconfig"
)

// fakeWebhookClient builds a dynamic fake client that can store (mutating|validating)
// webhook configurations.
func fakeWebhookClient(t *testing.T, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()

	scheme := runtime.NewScheme()

	err := admissionregistrationv1.AddToScheme(scheme)
	require.NoError(t, err)

	gvrToListKind := map[schema.GroupVersionResource]string{
		admissionregistrationv1.SchemeGroupVersion.
			WithResource(testMutatingGVRResource): "MutatingWebhookConfigurationList",
		admissionregistrationv1.SchemeGroupVersion.
			WithResource(testValidatingGVRResource): "ValidatingWebhookConfigurationList",
	}

	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

// applyTestWebhookConfig builds a ValidatingWebhookConfiguration with the given clientConfig
// service paths and the embedded manifests' failurePolicy.
func applyTestWebhookConfig(t *testing.T, name string, paths []string) *unstructured.Unstructured {
	t.Helper()

	entryName := "name"

	entries := make([]any, 0, len(paths))

	for _, path := range paths {
		entries = append(entries, map[string]any{
			entryName: path,
			"clientConfig": map[string]any{
				"service": map[string]any{
					entryName:   "webhook-service",
					"namespace": "system",
					"path":      path,
				},
			},
			"failurePolicy": "Fail",
		})
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata":   map[string]any{"name": name},
			"webhooks":   entries,
		},
	}

	return obj
}

// TestApplyWebhookProvidersDropsUnservedEntries proves the apply-time half of the fix: a
// webhook configuration whose entries mix servable and unserved paths is applied with only
// the servable entries, so the API server never dials a path the in-process webhook server
// would 404 (which, with the embedded failurePolicy: Fail, rejected every matching
// admission request — the AzureClusterIdentity outage this fixes).
func TestApplyWebhookProvidersDropsUnservedEntries(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	cache, err := NewProviderCache(runtime.NewScheme())
	require.NoError(t, err)

	cache.providerWebhooks = []unstructured.Unstructured{
		*applyTestWebhookConfig(t, "capz-validating-webhook-configuration", []string{
			testAzureClusterValidatePath,
			testAzureClusterIdentityValidatePath,
			testAzureManagedClusterValidatePath, // unserved: CRD denied
		}),
		*applyTestWebhookConfig(t, "capi-validating-webhook-configuration", []string{
			testExtensionConfigValidatePath, // unserved: internal handler
		}),
	}

	client := fakeWebhookClient(t)

	err = cache.ApplyWebhookProviders(ctx, "https://localhost:9443", []byte("ca"), client)
	require.NoError(t, err)

	vwc := client.Resource(admissionregistrationv1.SchemeGroupVersion.
		WithResource("validatingwebhookconfigurations"))

	// Mixed configuration: unserved entries gone, servable entries kept.
	mixed, err := vwc.Get(ctx, "capz-validating-webhook-configuration", metav1.GetOptions{})
	require.NoError(t, err)

	paths := webhookConfigPaths(t, mixed)
	assert.Equal(t, []string{
		testAzureClusterValidatePath,
		testAzureClusterIdentityValidatePath,
	}, paths)

	// Fully unserved configuration: not applied at all.
	_, err = vwc.Get(ctx, "capi-validating-webhook-configuration", metav1.GetOptions{})
	assert.Error(t, err, "a configuration with no servable entries must not be applied")
}

// TestApplyWebhookProvidersServesAllServableEntries proves a configuration whose entries
// are all servable is applied unchanged (no accidental dropping of live entries).
func TestApplyWebhookProvidersServesAllServableEntries(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	cache, err := NewProviderCache(runtime.NewScheme())
	require.NoError(t, err)

	servablePaths := []string{
		testAzureClusterValidatePath,
		testAzureClusterIdentityValidatePath,
	}

	cache.providerWebhooks = []unstructured.Unstructured{
		*applyTestWebhookConfig(t, "capz-validating-webhook-configuration", servablePaths),
	}

	client := fakeWebhookClient(t)

	err = cache.ApplyWebhookProviders(ctx, "https://localhost:9443", []byte("ca"), client)
	require.NoError(t, err)

	applied, err := client.Resource(admissionregistrationv1.SchemeGroupVersion.
		WithResource("validatingwebhookconfigurations")).
		Get(ctx, "capz-validating-webhook-configuration", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, servablePaths, webhookConfigPaths(t, applied))
}

// TestApplyWebhookProvidersRewritesClientConfig pins the clientConfig rewrite (service ->
// url + caBundle) that routes the webhook at the in-process server.
func TestApplyWebhookProvidersRewritesClientConfig(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	cache, err := NewProviderCache(runtime.NewScheme())
	require.NoError(t, err)

	cache.providerWebhooks = []unstructured.Unstructured{
		*applyTestWebhookConfig(t, "capz-validating-webhook-configuration", []string{
			testAzureClusterIdentityValidatePath,
		}),
	}

	client := fakeWebhookClient(t)

	err = cache.ApplyWebhookProviders(ctx, "https://localhost:9443", []byte("ca-bundle"), client)
	require.NoError(t, err)

	applied, err := client.Resource(admissionregistrationv1.SchemeGroupVersion.
		WithResource("validatingwebhookconfigurations")).
		Get(ctx, "capz-validating-webhook-configuration", metav1.GetOptions{})
	require.NoError(t, err)

	entries, found, err := unstructured.NestedSlice(applied.Object, "webhooks")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, entries, 1)

	entry, ok := entries[0].(map[string]any)
	require.True(t, ok)

	url, found, err := unstructured.NestedString(entry, "clientConfig", "url")
	require.NoError(t, err)
	require.True(t, found)

	assert.Equal(t, "https://localhost:9443/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureclusteridentity", url)

	caBundle, found, err := unstructured.NestedString(entry, "clientConfig", "caBundle")
	require.NoError(t, err)
	require.True(t, found)

	// CABundle is []byte, JSON-marshalled to base64.
	decoded, err := base64.StdEncoding.DecodeString(caBundle)
	require.NoError(t, err)

	assert.Equal(t, "ca-bundle", string(decoded))
}

// webhookConfigPaths extracts the admission paths of a webhook configuration's entries in
// declared order. Applied configurations carry the rewritten clientConfig.url
// (webhookURL + path), so the path is taken from there; the base URL is trimmed.
func webhookConfigPaths(t *testing.T, obj *unstructured.Unstructured) []string {
	t.Helper()

	entries, found, err := unstructured.NestedSlice(obj.Object, "webhooks")
	require.NoError(t, err)
	require.True(t, found)

	paths := make([]string, 0, len(entries))

	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		require.True(t, ok)

		fullURL, found, err := unstructured.NestedString(entryMap, "clientConfig", "url")
		require.NoError(t, err)
		require.True(t, found)

		parsed, err := url.Parse(fullURL)
		require.NoError(t, err)

		paths = append(paths, parsed.Path)
	}

	return paths
}
