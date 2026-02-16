package helpers

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
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
	ControlPlaneEndpointPort int
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
		"kommodity.controlplane.replicas":      1,
		"kommodity.nodepools.default.replicas": 1,
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

	scalewayDefaultZone := getNestedString(values, "kommodity.nodepools.default.zone")
	require.NotEmpty(t, scalewayDefaultZone, "kommodity.nodepools.default.zone must be set in %s", scalewayValuesFile)

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

// traverseNestedMap walks a dot-notation path (e.g. "kommodity.controlplane.replicas")
// to the parent map containing the leaf key, returning that map and the leaf key name.
// When create is true, missing intermediate maps are allocated along the path.
// When create is false and an intermediate key is missing, it returns (nil, "").
func traverseNestedMap(values map[string]any, path string, create bool) (map[string]any, string) {
	keys := strings.Split(path, ".")
	current := values

	// Walk all keys except the last one to reach the parent map.
	for _, key := range keys[:len(keys)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			if !create {
				return nil, ""
			}

			next = make(map[string]any)
			current[key] = next
		}

		current = next
	}

	return current, keys[len(keys)-1]
}

// setNestedValue sets a value at a dot-notation path in a nested map, creating intermediate maps as needed.
func setNestedValue(values map[string]any, path string, value any) {
	parent, key := traverseNestedMap(values, path, true)
	parent[key] = value
}

// getNestedString reads a string value at a dot-notation path, returning empty string if not found.
func getNestedString(values map[string]any, path string) string {
	parent, key := traverseNestedMap(values, path, false)
	if parent == nil {
		return ""
	}

	val, _ := parent[key].(string)

	return val
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
