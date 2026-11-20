// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"embed"
)

// conversionWebhookPath is the HTTP path the in-process conversion webhook server serves
// (see controller.NewAggregatedControllerManager, which registers "/convert"). The embedded
// CRDs all declare this same service path; it must stay in sync with the webhook server.
const conversionWebhookPath = "/convert"

// conversionWebhook is the strategy name indicating a CRD uses webhook conversion.
const conversionWebhook = "Webhook"

// unservedWebhookPaths are clientConfig paths of embedded admission-webhook entries that
// have no handler registered on the in-process webhook server (see SetupWebhooks in
// pkg/controller/webhook), so installing them would make every matching admission request
// fail with "failed calling webhook ... the server could not find the requested resource"
// (all embedded configurations use failurePolicy: Fail). The entries are filtered out of
// the applied webhook configurations in ApplyWebhookProviders.
//
// Each path must fall in one of two groups:
//   - The rule's resource is not installable in kommodity at all (e.g. CRDs on
//     providers.yaml's deny_list, like the azuremanaged*/azureasomanaged* kinds), so no
//     admission request can ever match the rule.
//   - The upstream handler lives in a Go package kommodity cannot import (e.g. CAPI's
//     internal/webhooks/runtime ExtensionConfig webhook). The resource is installed, so
//     admission silently passes without validation, matching upstream CAPZ/CAPI behaviour
//     for deployments that disable webhooks.
//
//nolint:gochecknoglobals // fixed allowlist of manifest-derived paths, never mutated.
var unservedWebhookPaths = map[string]struct{}{
	// Azure managed/ASO kinds are denied in providers.yaml; their CRDs are never installed.
	"/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcontrolplane":           {},
	"/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcontrolplanetemplate":   {},
	"/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedmachinepool":            {},
	"/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedmachinepooltemplate":    {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureasomanagedcluster":           {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureasomanagedcontrolplane":      {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azureasomanagedmachinepool":       {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcluster":              {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedclustertemplate":      {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcontrolplane":         {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedcontrolplanetemplate": {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedmachinepool":          {},
	"/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremanagedmachinepooltemplate":  {},
	// Runtime SDK is not enabled in kommodity, and the ExtensionConfig webhook handler
	// lives in cluster-api's internal/webhooks/runtime package, which cannot be imported.
	"/mutate-runtime-cluster-x-k8s-io-v1alpha1-extensionconfig":   {},
	"/validate-runtime-cluster-x-k8s-io-v1alpha1-extensionconfig": {},
}

//go:embed crds/**/*.yaml
var crds embed.FS

//go:embed webhooks/*.yaml
var webhooks embed.FS

// Cache caches provider CRDs to avoid redundant loading.
type Cache struct {
	decoder runtime.Serializer
	scheme  *runtime.Scheme

	providerCRDs     map[string][]unstructured.Unstructured
	providerWebhooks []unstructured.Unstructured
}

// NewProviderCache creates a new ProviderCache.
func NewProviderCache(scheme *runtime.Scheme) (*Cache, error) {
	return &Cache{
		decoder:          yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme),
		scheme:           scheme,
		providerCRDs:     make(map[string][]unstructured.Unstructured),
		providerWebhooks: make([]unstructured.Unstructured, 0),
	}, nil
}

// AddAllProvidersToScheme adds all provider CRDs to the given scheme.
func AddAllProvidersToScheme(scheme *runtime.Scheme) error {
	return addAllProvidersToScheme(scheme)
}

// GetProviderGroupResources returns a map of provider groups to their resource kinds.
func (pc *Cache) GetProviderGroupResources() map[string][]string {
	groups := make(map[string][]string)

	for group, objs := range pc.providerCRDs {
		kinds := make([]string, len(objs))
		for i, obj := range objs {
			kind, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "kind")
			kinds[i] = kind
		}

		groups[group] = kinds
	}

	return groups
}

// LoadCache loads all provider CRDs into the cache.
func (pc *Cache) LoadCache(ctx context.Context, cfg *config.KommodityConfig) error {
	err := pc.loadCRDCache(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to load CRD cache: %w", err)
	}

	err = pc.loadWebhookCache(ctx)
	if err != nil {
		return fmt.Errorf("failed to load webhook cache: %w", err)
	}

	return nil
}

// ApplyCRDProviders applies all provider CRDs to the given dynamic Kubernetes client.
//
//nolint:gocognit,funlen,cyclop,nestif,nolintlint
func (pc *Cache) ApplyCRDProviders(ctx context.Context,
	webhookURL string,
	webhookCRT []byte,
	client dynamic.Interface) error {
	logger := logging.FromContext(ctx)

	for group, objs := range pc.providerCRDs {
		logger.Info("Applying provider CRDs", zap.String("group", group), zap.Int("count", len(objs)))

		for _, obj := range objs {
			logger.Info("Applying CRD", zap.String("group", group))

			conversion, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "conversion")
			if found && conversion != nil {
				conversionStrategy, found, _ := unstructured.NestedString(obj.Object, "spec", "conversion", "strategy")
				if found && conversionStrategy == conversionWebhook {
					webhook, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "conversion", "webhook")
					if err != nil || !found {
						return fmt.Errorf("failed to extract webhook from crd configuration: %w", err)
					}

					webhookMap, success := webhook.(map[string]any)
					if !success {
						return ErrFailedToConvertWebhook
					}

					err = pc.updateWebhookWithClientData(webhookMap, webhookURL, webhookCRT)
					if err != nil {
						return fmt.Errorf("failed to update webhook with client data: %w", err)
					}

					err = unstructured.SetNestedField(obj.Object, webhookMap, "spec", "conversion", "webhook")
					if err != nil {
						return fmt.Errorf("failed to set webhook in crd configuration: %w", err)
					}
				}
			}

			crdGVR := apiextensionsv1.SchemeGroupVersion.
				WithResource("customresourcedefinitions")

			err := pc.load(ctx, client, crdGVR, &obj)
			if err != nil {
				return fmt.Errorf("failed to load CRD for group %s: %w", group, err)
			}
		}
	}

	return nil
}

// ReconcileConversionCABundles forcibly patches the conversion webhook clientConfig (URL + caBundle)
// on every conversion-webhook provider CRD via a JSON merge patch, so the caBundle always matches the
// current serving certificate — including on already-existing CRDs after a restart.
//
// A targeted merge patch is used instead of relying on the create-or-replace path in load(): a
// full-object Update of an existing CRD was observed to leave the stored caBundle untouched (the
// served cert and the persisted caBundle then diverge, breaking all CRD conversions). The merge
// patch is a server-side read-modify-write of just the clientConfig, which reliably updates it.
func (pc *Cache) ReconcileConversionCABundles(
	ctx context.Context,
	client dynamic.Interface,
	webhookURL string,
	webhookCRT []byte,
) error {
	logger := logging.FromContext(ctx)

	fingerprint := sha256.Sum256(webhookCRT)
	logger.Info("Reconciling conversion webhook caBundles",
		zap.String("certFingerprint", hex.EncodeToString(fingerprint[:8])))

	crdGVR := apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")

	for _, objs := range pc.providerCRDs {
		for _, obj := range objs {
			strategy, found, _ := unstructured.NestedString(obj.Object, "spec", "conversion", "strategy")
			if !found || strategy != conversionWebhook {
				continue
			}

			name := obj.GetName()

			// Set url + caBundle and remove the service reference (clientConfig must have exactly
			// one of url/service); a JSON merge patch deletes service via an explicit null.
			// The path is the fixed conversion endpoint, NOT clientConfig.service.path: by the time
			// this runs, ApplyCRDProviders has already replaced service with url on the shared
			// in-memory object, so service.path is empty — using it would drop "/convert" from the
			// URL and every multi-version conversion would hit the wrong path.
			patch := map[string]any{
				"spec": map[string]any{
					"conversion": map[string]any{
						"strategy": conversionWebhook,
						"webhook": map[string]any{
							"clientConfig": map[string]any{
								"url":      webhookURL + conversionWebhookPath,
								"caBundle": webhookCRT,
								"service":  nil,
							},
						},
					},
				},
			}

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				return fmt.Errorf("failed to marshal caBundle patch for %s: %w", name, err)
			}

			_, err = client.Resource(crdGVR).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
			if err != nil {
				return fmt.Errorf("failed to patch conversion caBundle for CRD %s: %w", name, err)
			}

			logger.Info("Patched conversion webhook caBundle", zap.String("crd", name))
		}
	}

	return nil
}

// ServedWebhookPaths returns the sorted, de-duplicated clientConfig paths of every
// embedded admission-webhook entry that is not in unservedWebhookPaths — i.e. exactly the
// paths ApplyWebhookProviders would install. Webhook setup cross-checks that a handler is
// registered for each of them (and fails startup otherwise), keeping the embedded
// manifests and the in-process webhook server in sync.
func ServedWebhookPaths() ([]string, error) {
	objs, err := readEmbeddedWebhookConfigurations()
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded webhook configurations: %w", err)
	}

	paths := make([]string, 0, len(objs))

	for _, obj := range objs {
		entryPaths, err := webhookEntryPaths(obj)
		if err != nil {
			return nil, err
		}

		for _, path := range entryPaths {
			if _, unserved := unservedWebhookPaths[path]; unserved {
				continue
			}

			if !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}

	slices.Sort(paths)

	return paths, nil
}

// webhookEntryPaths extracts the clientConfig service path of every entry in a webhook
// configuration object, in declared order.
func webhookEntryPaths(webhookConfig unstructured.Unstructured) ([]string, error) {
	webhooks, found, err := unstructured.NestedSlice(webhookConfig.Object, "webhooks")
	if err != nil || !found {
		return nil, fmt.Errorf("failed to extract webhooks from webhook configuration: %w", err)
	}

	paths := make([]string, 0, len(webhooks))

	for _, entry := range webhooks {
		entryMap, success := entry.(map[string]any)
		if !success {
			return nil, ErrFailedToConvertWebhook
		}

		path, found, err := unstructured.NestedString(entryMap, "clientConfig", "service", "path")
		if err != nil || !found {
			return nil, fmt.Errorf("failed to extract path from webhook configuration: %w", err)
		}

		paths = append(paths, path)
	}

	return paths, nil
}

// ApplyWebhookProviders applies all provider webhooks to the given dynamic Kubernetes client.
//
// Entries whose clientConfig path is in unservedWebhookPaths are dropped before applying:
// no handler for them is registered on the in-process webhook server, and with the embedded
// failurePolicy: Fail they would reject every matching admission request with "failed
// calling webhook ... the server could not find the requested resource". Dropping the entry
// makes admission silently pass instead — matching upstream behaviour for deployments that
// disable webhooks.
func (pc *Cache) ApplyWebhookProviders(ctx context.Context,
	webhookURL string,
	webhookCRT []byte,
	client dynamic.Interface) error {
	logger := logging.FromContext(ctx)

	logger.Info("Applying provider webhooks", zap.Int("count", len(pc.providerWebhooks)))

	for _, obj := range pc.providerWebhooks {
		dropped, err := dropUnservedWebhooks(ctx, &obj)
		if err != nil {
			return fmt.Errorf("failed to filter webhook %s: %w", obj.GetName(), err)
		}

		if dropped {
			logger.Info("Webhook configuration has no servable entries left, skipping",
				zap.String("name", obj.GetName()))

			continue
		}

		logger.Info("Applying webhook", zap.String("name", obj.GetName()))

		err = pc.updateWebhooks(&obj, webhookURL, webhookCRT)
		if err != nil {
			return fmt.Errorf("failed to update webhook %s with client data: %w", obj.GetName(), err)
		}

		webhookGVR := admissionregistrationv1.SchemeGroupVersion.
			WithResource("mutatingwebhookconfigurations")

		if obj.GetKind() == "ValidatingWebhookConfiguration" {
			webhookGVR = admissionregistrationv1.SchemeGroupVersion.
				WithResource("validatingwebhookconfigurations")
		}

		err = pc.load(ctx, client, webhookGVR, &obj)
		if err != nil {
			return fmt.Errorf("failed to load webhook %s: %w", obj.GetName(), err)
		}
	}

	return nil
}

// dropUnservedWebhooks removes every webhook entry whose clientConfig path is in
// unservedWebhookPaths from the given webhook configuration, in place. It reports whether
// no servable entries remain, in which case the whole configuration is skipped.
func dropUnservedWebhooks(ctx context.Context, webhookConfig *unstructured.Unstructured) (bool, error) {
	logger := logging.FromContext(ctx)

	allPaths, err := webhookEntryPaths(*webhookConfig)
	if err != nil {
		return false, err
	}

	servable := make([]string, 0, len(allPaths))
	servableSet := make(map[string]struct{}, len(allPaths))

	for _, path := range allPaths {
		if _, unserved := unservedWebhookPaths[path]; unserved {
			logger.Info("Dropping admission webhook entry with no registered handler",
				zap.String("name", webhookConfig.GetName()),
				zap.String("path", path))

			continue
		}

		servable = append(servable, path)
		servableSet[path] = struct{}{}
	}

	if len(servable) == len(allPaths) {
		return false, nil
	}

	if len(servable) == 0 {
		return true, nil
	}

	updated := make([]any, 0, len(servable))

	updated = append(updated, filterEntriesByPaths(webhookConfig, servableSet)...)

	err = unstructured.SetNestedSlice(webhookConfig.Object, updated, "webhooks")
	if err != nil {
		return false, fmt.Errorf("failed to set webhooks in webhook configuration: %w", err)
	}

	return false, nil
}

// filterEntriesByPaths returns the raw entries of the webhook configuration whose
// clientConfig service path is in the given set, preserving declared order.
func filterEntriesByPaths(webhookConfig *unstructured.Unstructured, keep map[string]struct{}) []any {
	entries, found, err := unstructured.NestedSlice(webhookConfig.Object, "webhooks")
	if err != nil || !found {
		return nil
	}

	filtered := make([]any, 0, len(entries))

	for _, entry := range entries {
		entryMap, success := entry.(map[string]any)
		if !success {
			continue
		}

		path, found, err := unstructured.NestedString(entryMap, "clientConfig", "service", "path")
		if err != nil || !found {
			continue
		}

		if _, ok := keep[path]; ok {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}

func (pc *Cache) updateWebhooks(webhook *unstructured.Unstructured, webhookURL string, webhookCRT []byte) error {
	webhooks, found, err := unstructured.NestedSlice(webhook.Object, "webhooks")
	if err != nil || !found {
		return fmt.Errorf("failed to extract webhooks from webhook configuration: %w", err)
	}

	for index := range webhooks {
		webhook, success := webhooks[index].(map[string]any)
		if !success {
			return ErrFailedToConvertWebhook
		}

		if _, exists := webhook["namespaceSelector"]; !exists {
			webhook["namespaceSelector"] = map[string]any{}
		}

		if _, exists := webhook["objectSelector"]; !exists {
			webhook["objectSelector"] = map[string]any{}
		}

		err := pc.updateWebhookWithClientData(webhook, webhookURL, webhookCRT)
		if err != nil {
			return fmt.Errorf("failed to update webhook with client data: %w", err)
		}
	}

	err = unstructured.SetNestedSlice(webhook.Object, webhooks, "webhooks")
	if err != nil {
		return fmt.Errorf("failed to set webhooks in webhook configuration: %w", err)
	}

	return nil
}

func (pc *Cache) updateWebhookWithClientData(webhook map[string]any, webhookURL string, webhookCRT []byte) error {
	path, found, err := unstructured.NestedString(webhook, "clientConfig", "service", "path")
	if err != nil || !found {
		return fmt.Errorf("failed to extract path from webhook configuration: %w", err)
	}

	url := webhookURL + path

	clientConfig := admissionregistrationv1.WebhookClientConfig{
		URL:      &url,
		CABundle: webhookCRT,
	}

	var clientConfigMap map[string]any

	b, err := json.Marshal(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal clientConfig: %w", err)
	}

	err = json.Unmarshal(b, &clientConfigMap)
	if err != nil {
		return fmt.Errorf("failed to unmarshal clientConfig: %w", err)
	}

	webhook["clientConfig"] = clientConfigMap

	return nil
}

func (pc *Cache) loadCRDCache(ctx context.Context, cfg *config.KommodityConfig) error {
	logger := logging.FromContext(ctx)

	entries, err := crds.ReadDir("crds")
	if err != nil {
		return fmt.Errorf("failed to read CRD directory: %w", err)
	}

	for _, provider := range entries {
		providerName := config.Provider(provider.Name())

		if !slices.Contains(cfg.InfrastructureProviders, providerName) {
			logger.Info("Skipping CRD, provider not in enabled providers",
				zap.String("provider", provider.Name()))

			continue
		}

		providerEntries, err := crds.ReadDir("crds/" + provider.Name())
		if err != nil {
			return fmt.Errorf("failed to read CRD provider directory %s: %w", provider.Name(), err)
		}

		for _, entry := range providerEntries {
			logger.Info("Loading CRD", zap.String("file", entry.Name()), zap.String("provider", provider.Name()))

			crd, err := crds.ReadFile("crds/" + provider.Name() + "/" + entry.Name())
			if err != nil {
				return fmt.Errorf("failed to read CRD file %s: %w", entry.Name(), err)
			}

			group, obj, err := pc.decodeCRD(crd)
			if err != nil {
				return fmt.Errorf("failed to decode CRD: %w", err)
			}

			err = stripDeprecatedVersions(ctx, obj)
			if err != nil {
				return fmt.Errorf("failed to strip deprecated versions: %w", err)
			}

			pc.loadCRDInScheme(group, obj)

			pc.providerCRDs[group] = append(pc.providerCRDs[group], *obj)
			logger.Info("Cached CRD", zap.String("group", group))
		}
	}

	return nil
}

// stripDeprecatedVersions keeps only the non-deprecated versions in spec.versions.
func stripDeprecatedVersions(ctx context.Context, obj *unstructured.Unstructured) error {
	logger := logging.FromContext(ctx)

	// Decode into typed CRD so version fields are compiler-checked.
	var crd apiextensionsv1.CustomResourceDefinition

	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd)
	if err != nil {
		return fmt.Errorf("failed to convert CRD %s: %w", obj.GetName(), err)
	}

	dropped := make([]string, 0)

	// Partition versions: keep non-deprecated, record dropped names for logging.
	kept := slices.DeleteFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
		if !v.Deprecated {
			return false
		}

		dropped = append(dropped, v.Name)

		return true
	})

	// Skip when nothing changed, or when every version is deprecated (avoid producing an invalid CRD).
	if len(dropped) == 0 || len(kept) == 0 {
		return nil
	}

	logger.Info("Stripped deprecated CRD versions",
		zap.String("crd", obj.GetName()),
		zap.Strings("versions", dropped))

	crd.Spec.Versions = kept

	updated, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&crd)
	if err != nil {
		return fmt.Errorf("failed to convert CRD %s back to unstructured: %w", obj.GetName(), err)
	}

	obj.Object = updated

	return nil
}

func (pc *Cache) loadCRDInScheme(group string, obj *unstructured.Unstructured) {
	kind, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "kind")
	versions, _, _ := unstructured.NestedSlice(obj.Object, "spec", "versions")

	for _, version := range versions {
		versionSpec, success := version.(map[string]any)
		if !success {
			continue
		}

		if served, success := versionSpec["served"].(bool); !success || !served {
			continue
		}

		versionName, success := versionSpec["name"].(string)
		if !success {
			continue
		}

		gvk := schema.GroupVersionKind{Group: group, Version: versionName, Kind: kind}

		known := pc.scheme.KnownTypes(gvk.GroupVersion())
		if _, exists := known[kind]; exists {
			continue
		}

		pc.scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		pc.scheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind(kind+"List"), &unstructured.UnstructuredList{})
	}
}

func (pc *Cache) loadWebhookCache(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	objs, err := readEmbeddedWebhookConfigurations()
	if err != nil {
		return fmt.Errorf("failed to read embedded webhook configurations: %w", err)
	}

	for _, obj := range objs {
		logger.Info("Cached webhook", zap.String("name", obj.GetName()))

		pc.providerWebhooks = append(pc.providerWebhooks, obj)
	}

	return nil
}

// readEmbeddedWebhookConfigurations decodes every embedded webhook manifest into
// unstructured objects.
func readEmbeddedWebhookConfigurations() ([]unstructured.Unstructured, error) {
	entries, err := webhooks.ReadDir("webhooks")
	if err != nil {
		return nil, fmt.Errorf("failed to read webhook directory: %w", err)
	}

	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	objs := make([]unstructured.Unstructured, 0, len(entries))

	for _, entry := range entries {
		manifest, err := webhooks.ReadFile("webhooks/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read webhook file %s: %w", entry.Name(), err)
		}

		obj := &unstructured.Unstructured{}

		_, _, err = decoder.Decode(manifest, nil, obj)
		if err != nil {
			return nil, fmt.Errorf("failed to decode YAML: %w", err)
		}

		objs = append(objs, *obj)
	}

	return objs, nil
}

func (pc *Cache) decodeCRD(crd []byte) (string, *unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}

	_, _, err := pc.decoder.Decode(crd, nil, obj)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode YAML: %w", err)
	}

	group, found, err := unstructured.NestedString(obj.Object, "spec", "group")
	if err != nil {
		return "", nil, fmt.Errorf("failed to extract group from CRD: %w", err)
	}

	if !found {
		return "", nil, ErrSpecGroupMissing
	}

	return group, obj, nil
}

func (pc *Cache) load(ctx context.Context,
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	obj *unstructured.Unstructured) error {
	_, err := client.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}

	if errors.IsAlreadyExists(err) {
		// Fetch the existing CRD to get its resourceVersion
		existing, getErr := client.Resource(gvr).Get(ctx, obj.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing CRD: %w", getErr)
		}

		// Set the resourceVersion to ensure we update the correct version
		obj.SetResourceVersion(existing.GetResourceVersion())

		_, err = client.Resource(gvr).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update CRD: %w", err)
		}

		return nil
	}

	return fmt.Errorf("failed to create CRD: %w", err)
}
