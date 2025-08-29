// Package provider contains utilities for working with provider CRDs.
package provider

import (
	"context"
	"fmt"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/dynamic"

	_ "embed"
)

//go:embed crds.yaml
var crds string

// ApplyAllProviders applies all provider CRDs to the given dynamic Kubernetes client.
func ApplyAllProviders(client *dynamic.DynamicClient) error {
	decUnstructured := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	crdGVR := apiextensionsv1.SchemeGroupVersion.
		WithResource("customresourcedefinitions")

	crdList := strings.Split(crds, "---")
	for _, crd := range crdList {
		if strings.TrimSpace(crd) == "" {
			continue
		}

		obj := &unstructured.Unstructured{}

		_, _, err := decUnstructured.Decode([]byte(crd), nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode YAML: %w", err)
		}

		name := obj.GetName()
		if name == "" {
			return ErrMissingCRDName
		}

		_, err = client.Resource(crdGVR).Apply(context.Background(), name, obj, metav1.ApplyOptions{
			FieldManager: "kommodity-provider",
		})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				continue // Ignore already exists error
			}

			return fmt.Errorf("failed to create CRD: %w", err)
		}
	}

	return nil
}
