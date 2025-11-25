// Package openapi is a wrapper around
// - core/v1,
// - meta/v1,
// - batch/v1,
// - apimachinery/pkg/runtime
// - apimachinery/pkg/version
// - apiextensions-apiserver/apiextensions/v1
// to generate OpenAPI specs for those.
package openapi

//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./core --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/core --output-file=zz_generated.openapi.go --logtostderr k8s.io/api/core/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./meta --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/meta --output-file=zz_generated.openapi.go --logtostderr k8s.io/apimachinery/pkg/apis/meta/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./runtime --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/runtime --output-file=zz_generated.openapi.go --logtostderr k8s.io/apimachinery/pkg/runtime
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./version --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/version --output-file=zz_generated.openapi.go --logtostderr k8s.io/apimachinery/pkg/version
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./apiextensions --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/apiextensions --output-file=zz_generated.openapi.go --logtostderr k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./apiregistration --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/apiregistration --output-file=zz_generated.openapi.go --logtostderr k8s.io/kube-aggregator/pkg/apis/apiregistration/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./admissionregistration --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/admissionregistration --output-file=zz_generated.openapi.go --logtostderr k8s.io/api/admissionregistration/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./intstr --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/intstr --output-file=zz_generated.openapi.go --logtostderr k8s.io/apimachinery/pkg/util/intstr
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./authorization --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/authorization --output-file=zz_generated.openapi.go --logtostderr k8s.io/api/authorization/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./batch --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/batch --output-file=zz_generated.openapi.go --logtostderr k8s.io/api/batch/v1

import (
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/openapi/admissionregistration"
	"github.com/kommodity-io/kommodity/pkg/openapi/apiextensions"
	"github.com/kommodity-io/kommodity/pkg/openapi/authorization"
	"github.com/kommodity-io/kommodity/pkg/openapi/batch"
	"github.com/kommodity-io/kommodity/pkg/openapi/core"
	"github.com/kommodity-io/kommodity/pkg/openapi/intstr"
	"github.com/kommodity-io/kommodity/pkg/openapi/meta"
	"github.com/kommodity-io/kommodity/pkg/openapi/runtime"
	"github.com/kommodity-io/kommodity/pkg/openapi/version"

	yaml "go.yaml.in/yaml/v3"

	_ "embed"

	// Used for OpenAPI generation using openapi-gen.
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kube-openapi/pkg/common"
)

//go:embed types.yaml
var openAPITypes string

// Spec contains the desired OpenAPI spec types defined in types.yaml.
type Spec struct {
	Types map[string][]string `yaml:"types"`
}

// NewOpenAPISpec returns the OpenAPI specifications defined in types.yaml.
func NewOpenAPISpec() (*Spec, error) {
	types := Spec{}

	err := yaml.Unmarshal([]byte(openAPITypes), &types)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling types.yaml: %w", err)
	}

	return &types, nil
}

// GetOpenAPIDefinitions retrieves the OpenAPI definitions defined in types.yaml.
func (o *Spec) GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	kubernetesOpenAPIDefinitions := map[string]map[string]common.OpenAPIDefinition{
		"core":                  core.GetOpenAPIDefinitions(ref),
		"meta":                  meta.GetOpenAPIDefinitions(ref),
		"version":               version.GetOpenAPIDefinitions(ref),
		"runtime":               runtime.GetOpenAPIDefinitions(ref),
		"apiextensions":         apiextensions.GetOpenAPIDefinitions(ref),
		"intstr":                intstr.GetOpenAPIDefinitions(ref),
		"authorization":         authorization.GetOpenAPIDefinitions(ref),
		"admissionregistration": admissionregistration.GetOpenAPIDefinitions(ref),
		"batch":                 batch.GetOpenAPIDefinitions(ref),
	}

	openAPIDefinition := make(map[string]common.OpenAPIDefinition)

	for openAPIGroup, types := range o.Types {
		for _, openAPIType := range types {
			openAPIDefinition[openAPIType] = kubernetesOpenAPIDefinitions[openAPIGroup][openAPIType]
		}
	}

	return openAPIDefinition
}
