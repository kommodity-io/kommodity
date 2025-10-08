// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
	"fmt"

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

//go:embed crds/*.yaml
var crds embed.FS

//go:embed webhooks/*.yaml
var webhooks embed.FS

// Cache caches provider CRDs to avoid redundant loading.
type Cache struct {
	decoder runtime.Serializer

	providerCRDs     map[string][]unstructured.Unstructured
	providerWebhooks []unstructured.Unstructured
}

// NewProviderCache creates a new ProviderCache.
func NewProviderCache(scheme *runtime.Scheme) (*Cache, error) {
	err := addAllProvidersToScheme(scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add providers to scheme: %w", err)
	}

	return &Cache{
		decoder:          yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme),
		providerCRDs:     make(map[string][]unstructured.Unstructured),
		providerWebhooks: make([]unstructured.Unstructured, 0),
	}, nil
}

// GetProviderGroups returns the groups of all cached providers.
func (pc *Cache) GetProviderGroups() []string {
	groups := make([]string, 0, len(pc.providerCRDs))

	for group := range pc.providerCRDs {
		groups = append(groups, group)
	}

	return groups
}

// LoadCache loads all provider CRDs into the cache.
func (pc *Cache) LoadCache(ctx context.Context) error {
	err := pc.loadCRDCache(ctx)
	if err != nil {
		return fmt.Errorf("failed to load CRD cache: %w", err)
	}

	err = pc.loadWebhookCache(ctx)
	if err != nil {
		return fmt.Errorf("failed to load webhook cache: %w", err)
	}

	return nil
}

// ApplyAllProviders applies all provider CRDs to the given dynamic Kubernetes client.
func (pc *Cache) ApplyAllProviders(ctx context.Context, client *dynamic.DynamicClient) error {
	logger := logging.FromContext(ctx)

	for group, objs := range pc.providerCRDs {
		logger.Info("Applying provider CRDs", zap.String("group", group), zap.Int("count", len(objs)))

		for _, obj := range objs {
			logger.Info("Applying CRD", zap.String("group", group))

			crdGVR := apiextensionsv1.SchemeGroupVersion.
				WithResource("customresourcedefinitions")

			err := pc.load(ctx, client, crdGVR, &obj)
			if err != nil {
				return fmt.Errorf("failed to load CRD for group %s: %w", group, err)
			}
		}
	}

	logger.Info("Applying provider webhooks", zap.Int("count", len(pc.providerWebhooks)))

	for _, obj := range pc.providerWebhooks {
		logger.Info("Applying webhook", zap.String("name", obj.GetName()))

		webhookGVR := admissionregistrationv1.SchemeGroupVersion.
			WithResource("mutatingwebhookconfigurations")

		if obj.GetKind() == "ValidatingWebhookConfiguration" {
			webhookGVR = admissionregistrationv1.SchemeGroupVersion.
				WithResource("validatingwebhookconfigurations")
		}

		err := pc.load(ctx, client, webhookGVR, &obj)
		if err != nil {
			return fmt.Errorf("failed to load webhook %s: %w", obj.GetName(), err)
		}
	}

	return nil
}

func (pc *Cache) loadCRDCache(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	entries, err := crds.ReadDir("crds")
	if err != nil {
		return fmt.Errorf("failed to read CRD directory: %w", err)
	}

	for _, entry := range entries {
		logger.Info("Loading CRD", zap.String("file", entry.Name()))

		crd, err := crds.ReadFile("crds/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read CRD file %s: %w", entry.Name(), err)
		}

		group, obj, err := pc.decodeCRD(crd)
		if err != nil {
			return fmt.Errorf("failed to decode CRD: %w", err)
		}

		pc.providerCRDs[group] = append(pc.providerCRDs[group], *obj)
		logger.Info("Cached CRD", zap.String("group", group))
	}

	return nil
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
