// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"embed"
)

//go:embed crds/*.yaml
var crds embed.FS

// ApplyAllProviders applies all provider CRDs to the given dynamic Kubernetes client.
func ApplyAllProviders(client *dynamic.DynamicClient, ctx context.Context) error {
	logger := logging.FromContext(ctx)

	entries, err := crds.ReadDir("crds")
	if err != nil {
		return fmt.Errorf("failed to read CRD directory: %w", err)
	}

	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	for _, entry := range entries {

		logger.Info("Applying CRD", zap.String("file", entry.Name()))
		data, err := crds.ReadFile("crds/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read CRD file %s: %w", entry.Name(), err)
		}

		crdList := strings.Split(string(data), "---")
		for _, crd := range crdList {
			if strings.TrimSpace(crd) == "" {
				continue
			}

			err = loadCRD([]byte(crd), decoder, client)
			if err != nil {
				return fmt.Errorf("failed to load CRD: %w", err)
			}
		}
	}

	err = addAllProvidersToScheme(clientgoscheme.Scheme)
	if err != nil {
		return fmt.Errorf("failed to add providers to scheme: %w", err)
	}

	err = addAllProvidersToScheme(apiextensionsapiserver.Scheme)
	if err != nil {
		return fmt.Errorf("failed to add apiextensions to scheme: %w", err)
	}

	return nil
}

func loadCRD(crd []byte, decoder runtime.Serializer, client *dynamic.DynamicClient) error {
	obj := &unstructured.Unstructured{}

	crdGVR := apiextensionsv1.SchemeGroupVersion.
		WithResource("customresourcedefinitions")

	_, _, err := decoder.Decode(crd, nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	_, err = client.Resource(crdGVR).Create(context.Background(), obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}

	if errors.IsAlreadyExists(err) {
		// Fetch the existing CRD to get its resourceVersion
		existing, getErr := client.Resource(crdGVR).Get(context.Background(), obj.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing CRD: %w", getErr)
		}

		// Set the resourceVersion to ensure we update the correct version
		obj.SetResourceVersion(existing.GetResourceVersion())

		_, err = client.Resource(crdGVR).Update(context.Background(), obj, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update CRD: %w", err)
		}

		return nil
	}

	return fmt.Errorf("failed to create CRD: %w", err)
}
