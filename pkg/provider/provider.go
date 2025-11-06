// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
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
	"k8s.io/client-go/dynamic"

	"embed"
)

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
	client *dynamic.DynamicClient) error {
	logger := logging.FromContext(ctx)

	for group, objs := range pc.providerCRDs {
		logger.Info("Applying provider CRDs", zap.String("group", group), zap.Int("count", len(objs)))

		for _, obj := range objs {
			logger.Info("Applying CRD", zap.String("group", group))

			conversion, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "conversion")
			if found && conversion != nil {
				conversionStrategy, found, _ := unstructured.NestedString(obj.Object, "spec", "conversion", "strategy")
				if found && conversionStrategy == "Webhook" {
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

// ApplyWebhookProviders applies all provider webhooks to the given dynamic Kubernetes client.
func (pc *Cache) ApplyWebhookProviders(ctx context.Context,
	webhookURL string,
	webhookCRT []byte,
	client *dynamic.DynamicClient) error {
	logger := logging.FromContext(ctx)

	logger.Info("Applying provider webhooks", zap.Int("count", len(pc.providerWebhooks)))

	for _, obj := range pc.providerWebhooks {
		logger.Info("Applying webhook", zap.String("name", obj.GetName()))

		err := pc.updateWebhooks(&obj, webhookURL, webhookCRT)
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
			webhook["namespaceSelector"] = map[string]interface{}{}
		}

		if _, exists := webhook["objectSelector"]; !exists {
			webhook["objectSelector"] = map[string]interface{}{}
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

	var clientConfigMap map[string]interface{}

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
			logger.Info("Skipping CRD provider not in enabled providers",
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

			pc.loadCRDInScheme(group, obj)

			pc.providerCRDs[group] = append(pc.providerCRDs[group], *obj)
			logger.Info("Cached CRD", zap.String("group", group))
		}
	}

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

	entries, err := webhooks.ReadDir("webhooks")
	if err != nil {
		return fmt.Errorf("failed to read webhook directory: %w", err)
	}

	for _, entry := range entries {
		logger.Info("Loading webhook", zap.String("file", entry.Name()))

		webhook, err := webhooks.ReadFile("webhooks/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read webhook file %s: %w", entry.Name(), err)
		}

		obj := &unstructured.Unstructured{}

		_, _, err = pc.decoder.Decode(webhook, nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode YAML: %w", err)
		}

		pc.providerWebhooks = append(pc.providerWebhooks, *obj)

		logger.Info("Cached webhook", zap.String("name", obj.GetName()))
	}

	return nil
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
	client *dynamic.DynamicClient,
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
