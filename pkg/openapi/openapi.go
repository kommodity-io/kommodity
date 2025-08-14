package openapi

//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./core --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/core --output-file=zz_generated.openapi.go --logtostderr k8s.io/api/core/v1
//go:generate go run k8s.io/kube-openapi/cmd/openapi-gen --output-dir=./meta --output-pkg=github.com/kommodity-io/kommodity/pkg/openapi/meta --output-file=zz_generated.openapi.go --logtostderr k8s.io/apimachinery/pkg/apis/meta/v1

import (
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/openapi/core"
	"github.com/kommodity-io/kommodity/pkg/openapi/meta"
	yaml "gopkg.in/yaml.v3"

	_ "embed"

	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kube-openapi/pkg/common"
)

//go:embed types.yaml
var openAPITypes string

type OpenAPISpec struct {
	Types map[string][]string `yaml:"types"`
}

func NewOpenAPISpec() (*OpenAPISpec, error) {
	types := OpenAPISpec{}

	err := yaml.Unmarshal([]byte(openAPITypes), &types)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling types.yaml: %v", err)
	}

	return &types, nil
}

func (o *OpenAPISpec) GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	kubernetesOpenAPIDefinitions := map[string]map[string]common.OpenAPIDefinition{
		"core": core.GetOpenAPIDefinitions(ref),
		"meta": meta.GetOpenAPIDefinitions(ref),
	}

	openAPIDefinition := make(map[string]common.OpenAPIDefinition)

	for openAPIGroup, types := range o.Types {
		for _, openAPIType := range types {
			openAPIDefinition[openAPIType] = kubernetesOpenAPIDefinitions[openAPIGroup][openAPIType]
		}
	}

	return openAPIDefinition
}
