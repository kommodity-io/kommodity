package helpers

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	kubevirtControlPlaneEndpointHost = "10.0.0.1"
	kubevirtControlPlaneEndpointPort = 6443
	kubevirtValuesFile               = "values.kubevirt.yaml"
	scalewayValuesFile               = "values.scaleway.yaml"
	scalewayTestSKU                  = "DEV1-S"
)

// Infrastructure defines provider-specific Helm value overrides for cluster chart installation.
type Infrastructure interface {
	ValuesFile() string
	Overrides() map[string]any
}

// ScalewayInfra holds Scaleway-specific configuration for chart installation.
type ScalewayInfra struct {
	ProjectID string
}

// ValuesFile returns the Helm values file for Scaleway.
func (s ScalewayInfra) ValuesFile() string { return scalewayValuesFile }

// Overrides returns the Helm value overrides for Scaleway testing.
func (s ScalewayInfra) Overrides() map[string]any {
	return map[string]any{
		"kommodity.nodepools.default.sku":     scalewayTestSKU,
		"kommodity.controlplane.sku":          scalewayTestSKU,
		"kommodity.provider.config.projectID": s.ProjectID,
		"kommodity.network.ipv4.nodeCIDR":     nil,
	}
}

// KubevirtInfra holds KubeVirt-specific configuration for chart installation.
type KubevirtInfra struct {
	InfraClusterNamespace    string
	ControlPlaneEndpointHost string
	ControlPlaneEndpointPort int64
}

// ValuesFile returns the Helm values file for KubeVirt.
func (k KubevirtInfra) ValuesFile() string { return kubevirtValuesFile }

// Overrides returns the Helm value overrides for KubeVirt testing.
func (k KubevirtInfra) Overrides() map[string]any {
	return map[string]any{
		"kommodity.provider.config.infraClusterNamespace": k.InfraClusterNamespace,
		"kommodity.provider.config.controlPlaneEndpoint": map[string]any{
			"host": k.ControlPlaneEndpointHost,
			"port": k.ControlPlaneEndpointPort,
		},
		"kommodity.controlplane.replicas":      int64(1),
		"kommodity.nodepools.default.replicas": int64(1),
	}
}

// installKommodityClusterChart loads the kommodity-cluster Helm chart, applies the infrastructure
// overrides, and installs the chart. Returns the loaded values for caller inspection.
func installKommodityClusterChart(
	t *testing.T,
	env TestEnvironment,
	releaseName string,
	namespace string,
	infra Infrastructure,
) chartutil.Values {
	t.Helper()

	repoRoot, err := FindRepoRoot()
	require.NoError(t, err)

	chartPath := filepath.Join(repoRoot, "charts", "kommodity-cluster")
	valuesPath := filepath.Join(repoRoot, "charts", "kommodity-cluster", infra.ValuesFile())

	cfg := new(action.Configuration)
	restGetter := genericclioptions.NewConfigFlags(false)
	apiServer := env.KommodityCfg.Host
	restGetter.APIServer = &apiServer
	restGetter.Namespace = &namespace

	err = cfg.Init(restGetter, namespace, "secret", func(string, ...any) {})
	require.NoError(t, err)

	chart, err := loader.Load(chartPath)
	require.NoError(t, err)

	values, err := chartutil.ReadValuesFile(valuesPath)
	require.NoError(t, err)

	for key, value := range infra.Overrides() {
		setNestedValue(values, key, value)
	}

	installer := action.NewInstall(cfg)
	installer.ReleaseName = releaseName
	installer.Namespace = namespace
	installer.Wait = false

	_, err = installer.Run(chart, values)
	require.NoError(t, err)

	return values
}

// InstallKommodityClusterChartScaleway installs the kommodity-cluster helm chart with Scaleway values.
func InstallKommodityClusterChartScaleway(
	t *testing.T,
	env TestEnvironment,
	releaseName string,
	namespace string,
	scalewayProjectID string,
) string {
	t.Helper()

	values := installKommodityClusterChart(t, env, releaseName, namespace, ScalewayInfra{
		ProjectID: scalewayProjectID,
	})

	scalewayDefaultZone, err := getNestedString(values, "kommodity.nodepools.default.zone")
	require.NoError(t, err)

	return scalewayDefaultZone
}

// InstallKommodityClusterChartKubevirt installs the kommodity-cluster helm chart with KubeVirt values.
func InstallKommodityClusterChartKubevirt(
	t *testing.T,
	env TestEnvironment,
	releaseName string,
	namespace string,
	infraClusterNamespace string,
) {
	t.Helper()

	installKommodityClusterChart(t, env, releaseName, namespace, KubevirtInfra{
		InfraClusterNamespace:    infraClusterNamespace,
		ControlPlaneEndpointHost: kubevirtControlPlaneEndpointHost,
		ControlPlaneEndpointPort: kubevirtControlPlaneEndpointPort,
	})
}

// setNestedValue sets a value at a dot-notation path in a nested map, creating intermediate maps as needed.
func setNestedValue(values map[string]any, path string, value any) {
	_ = unstructured.SetNestedField(values, value, strings.Split(path, ".")...)
}

// getNestedString reads a string value at a dot-notation path, returning empty string if not found.
func getNestedString(values map[string]any, path string) (string, error) {
	val, found, err := unstructured.NestedString(values, strings.Split(path, ".")...)
	if !found || err != nil {
		return "", fmt.Errorf("failed to get nested string at path %q: %w", path, err)
	}

	return val, nil
}

// UninstallKommodityClusterChart uninstalls the kommodity-cluster helm chart with the specified parameters.
func UninstallKommodityClusterChart(t *testing.T, env TestEnvironment, releaseName string, namespace string) {
	t.Helper()

	cfg := new(action.Configuration)
	restGetter := genericclioptions.NewConfigFlags(false)
	apiServer := env.KommodityCfg.Host
	restGetter.APIServer = &apiServer
	restGetter.Namespace = &namespace

	err := cfg.Init(restGetter, namespace, "secret", func(string, ...any) {})
	require.NoError(t, err)

	uninstaller := action.NewUninstall(cfg)

	_, err = uninstaller.Run(releaseName)
	require.NoError(t, err)
}
