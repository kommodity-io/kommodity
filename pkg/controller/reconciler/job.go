package reconciler

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/Masterminds/sprig/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// InstallModeHelmInstall indicates that the Helm chart should be installed using Helm.
	InstallModeHelmInstall = "HelmInstall"
	// InstallModeKubectlApply indicates that the Helm chart should be installed using kubectl apply.
	InstallModeKubectlApply = "KubectlApply"
)

//go:embed job.tmpl
var jobTmplFS embed.FS

// Config holds the configuration for rendering the Helm install job template.
type Config struct {
	Name            string
	Namespace       string
	Chart           Chart
	InstallMode     string
	Condition       Condition
	UpgradeDisabled bool
	HasExtraValues  bool
}

// Chart holds the Helm chart information.
type Chart struct {
	Name       string
	Version    string
	Repository string
}

// Condition holds the condition information for the Helm install job.
type Condition struct {
	Defined     bool
	Kind        string
	MatchLabels map[string]string
}

// NewHelmInstallConfig creates a new Config instance for Helm installation.
func NewHelmInstallConfig(name, namespace, chartName, chartVersion,
	chartRepository string, hasExtraValues bool) Config {
	return Config{
		Name:      name,
		Namespace: namespace,
		Chart: Chart{
			Name:       chartName,
			Version:    chartVersion,
			Repository: chartRepository,
		},
		Condition: Condition{
			Defined: false,
		},
		InstallMode:     InstallModeHelmInstall,
		UpgradeDisabled: true,
		HasExtraValues:  hasExtraValues,
	}
}

// ApplyTemplate renders the Helm install job template and applies the resulting resources to the Kubernetes cluster.
func (c *Config) ApplyTemplate(ctx context.Context, kubeClient client.Client) error {
	templated, err := c.getTemplatedJob()
	if err != nil {
		return fmt.Errorf("failed to get templated job: %w", err)
	}

	docs := strings.Split(templated, "---")
	dec := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		obj := &unstructured.Unstructured{}

		_, _, err := dec.Decode([]byte(doc), nil, obj)
		if err != nil {
			return fmt.Errorf("failed to decode rendered job document: %w", err)
		}

		err = kubeClient.Create(ctx, obj)
		if apierrors.IsAlreadyExists(err) {
			err := kubeClient.Update(ctx, obj)
			if err != nil {
				return fmt.Errorf("failed to update autoscaler resource %s/%s: %w",
					obj.GetNamespace(), obj.GetName(), err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to create autoscaler resource %s/%s: %w",
				obj.GetNamespace(), obj.GetName(), err)
		}
	}

	return nil
}

func (c *Config) getTemplatedJob() (string, error) {
	funcs := sprig.FuncMap()
	funcs["getFullName"] = func() string {
		return c.getFullName()
	}

	tpl := template.Must(template.New("job.tmpl").
		Funcs(funcs).
		ParseFS(jobTmplFS, "job.tmpl"))

	var buf bytes.Buffer

	err := tpl.Execute(&buf, c)
	if err != nil {
		return "", fmt.Errorf("failed to render job template: %w", err)
	}

	return buf.String(), nil
}

func (c *Config) getFullName() string {
	return fmt.Sprintf("%s-%s", c.Name, strings.ReplaceAll(c.Chart.Version, ".", "-"))
}
