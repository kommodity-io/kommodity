// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/dynamic"

	"embed"
)

//go:embed crds/*.yaml
var crds embed.FS

// Cache caches provider CRDs to avoid redundant loading.
type Cache struct {
	decoder runtime.Serializer

	providers map[string][]unstructured.Unstructured
}

// NewProviderCache creates a new ProviderCache.
func NewProviderCache(scheme *runtime.Scheme) (*Cache, error) {
	err := addAllProvidersToScheme(scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add providers to scheme: %w", err)
	}

	return &Cache{
		decoder:   yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme),
		providers: make(map[string][]unstructured.Unstructured),
	}, nil
}

// GetProviderGroups returns the groups of all cached providers.
func (pc *Cache) GetProviderGroups() []string {
	groups := make([]string, 0, len(pc.providers))

	for group := range pc.providers {
		groups = append(groups, group)
	}

	return groups
}

// LoadCache loads all provider CRDs into the cache.
func (pc *Cache) LoadCache(ctx context.Context) error {
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

		pc.providers[group] = append(pc.providers[group], *obj)
		logger.Info("Cached CRD", zap.String("group", group))
	}

	return nil
}

// ApplyAllProviders applies all provider CRDs to the given dynamic Kubernetes client.
func (pc *Cache) ApplyAllProviders(ctx context.Context, client *dynamic.DynamicClient) error {
	logger := logging.FromContext(ctx)

	for group, objs := range pc.providers {
		logger.Info("Applying provider CRDs", zap.String("group", group), zap.Int("count", len(objs)))

		for _, obj := range objs {
			logger.Info("Applying CRD", zap.String("group", group))

			err := pc.loadCRD(ctx, client, &obj)
			if err != nil {
				return fmt.Errorf("failed to load CRD for group %s: %w", group, err)
			}
		}
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

func (pc *Cache) loadCRD(ctx context.Context, client *dynamic.DynamicClient, crd *unstructured.Unstructured) error {
	crdGVR := apiextensionsv1.SchemeGroupVersion.
		WithResource("customresourcedefinitions")

	_, err := client.Resource(crdGVR).Create(ctx, crd, metav1.CreateOptions{})
	if err == nil {
		return nil
	}

	if errors.IsAlreadyExists(err) {
		// Fetch the existing CRD to get its resourceVersion
		existing, getErr := client.Resource(crdGVR).Get(ctx, crd.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing CRD: %w", getErr)
		}

		// Set the resourceVersion to ensure we update the correct version
		crd.SetResourceVersion(existing.GetResourceVersion())

		_, err = client.Resource(crdGVR).Update(ctx, crd, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update CRD: %w", err)
		}

		return nil
	}

	return fmt.Errorf("failed to create CRD: %w", err)
}
