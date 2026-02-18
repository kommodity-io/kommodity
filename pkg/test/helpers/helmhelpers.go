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
	kubevirtControlPlaneEndpointHost = "10.0.0.1"
	kubevirtControlPlaneEndpointPort = 6443
)

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
		err := setNestedValue(values, key, value)
		require.NoError(t, err)
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
