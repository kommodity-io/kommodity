package helpers

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	controlPlaneEndpointHost = "10.0.0.1"
	controlPlaneEndpointPort = 6443
)

// installKommodityClusterChart loads the kommodity-cluster Helm chart, applies the given values file,
// runs the modifier to adjust values, and installs the chart.
func installKommodityClusterChart(
	t *testing.T,
	env TestEnvironment,
	releaseName string,
	namespace string,
	valuesFile string,
	modifier func(values chartutil.Values),
) {
	t.Helper()

	repoRoot, err := FindRepoRoot()
	require.NoError(t, err)

	chartPath := filepath.Join(repoRoot, "charts", "kommodity-cluster")
	valuesPath := filepath.Join(repoRoot, "charts", "kommodity-cluster", valuesFile)

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

	modifier(values)

	installer := action.NewInstall(cfg)
	installer.ReleaseName = releaseName
	installer.Namespace = namespace
	installer.Wait = false

	_, err = installer.Run(chart, values)
	require.NoError(t, err)
}

// InstallKommodityClusterChart installs the kommodity-cluster helm chart with Scaleway values.
func InstallKommodityClusterChart(
	t *testing.T,
	env TestEnvironment,
	releaseName string,
	namespace string,
	valuesFile string,
	scalewayProjectID string,
) string {
	t.Helper()

	var scalewayDefaultZone string

	installKommodityClusterChart(t, env, releaseName, namespace, valuesFile, func(values chartutil.Values) {
		scalewayDefaultZone = modifyScalewayValues(values, scalewayProjectID)
	})

	require.NotEmpty(t, scalewayDefaultZone, "kommodity.nodepools.default.zone must be set in %s", valuesFile)

	return scalewayDefaultZone
}

// modifyScalewayValues adjusts Helm values for Scaleway testing and returns the default zone.
func modifyScalewayValues(values chartutil.Values, scalewayProjectID string) string {
	scalewayDefaultZone := ""

	kommoditySection, ok := values["kommodity"].(map[string]any)
	if !ok {
		return scalewayDefaultZone
	}

	if nodepools, ok := kommoditySection["nodepools"].(map[string]any); ok {
		if defaultPool, ok := nodepools["default"].(map[string]any); ok {
			defaultPool["sku"] = "DEV1-S"

			if zone, ok := defaultPool["zone"].(string); ok {
				scalewayDefaultZone = zone
			}
		}
	}

	if controlplane, ok := kommoditySection["controlplane"].(map[string]any); ok {
		controlplane["sku"] = "DEV1-S"
	}

	if provider, ok := kommoditySection["provider"].(map[string]any); ok {
		if config, ok := provider["config"].(map[string]any); ok {
			config["projectID"] = scalewayProjectID
		}
	}

	if network, ok := kommoditySection["network"].(map[string]any); ok {
		if ipv4, ok := network["ipv4"].(map[string]any); ok {
			ipv4["nodeCIDR"] = nil
		}
	}

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

	installKommodityClusterChart(t, env, releaseName, namespace, "values.kubevirt.yaml", func(values chartutil.Values) {
		modifyKubevirtValues(values, infraClusterNamespace)
	})
}

// modifyKubevirtValues adjusts Helm values for KubeVirt testing.
func modifyKubevirtValues(values chartutil.Values, infraClusterNamespace string) {
	kommoditySection, ok := values["kommodity"].(map[string]any)
	if !ok {
		return
	}

	if provider, ok := kommoditySection["provider"].(map[string]any); ok {
		if config, ok := provider["config"].(map[string]any); ok {
			config["infraClusterNamespace"] = infraClusterNamespace
			config["controlPlaneEndpoint"] = map[string]any{
				"host": controlPlaneEndpointHost,
				"port": controlPlaneEndpointPort,
			}
		}
	}

	if controlplane, ok := kommoditySection["controlplane"].(map[string]any); ok {
		controlplane["replicas"] = 1
	}

	if nodepools, ok := kommoditySection["nodepools"].(map[string]any); ok {
		if defaultPool, ok := nodepools["default"].(map[string]any); ok {
			defaultPool["replicas"] = 1
		}
	}
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
