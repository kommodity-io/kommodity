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

// InstallKommodityClusterChart installs the kommodity-cluster helm chart with the specified parameters.
//nolint:funlen // Function length is acceptable for a test helper.
func InstallKommodityClusterChart(t *testing.T, env TestEnvironment,
	releaseName string, namespace string, valuesFile string, scalewayProjectID string) string {
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

	scalewayDefaultZone := ""
	
	//nolint:nestif // Nested ifs are acceptable in this case for clarity.
	if kommoditySection, ok := values["kommodity"].(map[string]any); ok {
		if nodepools, ok := kommoditySection["nodepools"].(map[string]any); ok {
			if defaultPool, ok := nodepools["default"].(map[string]any); ok {
				// Set SKU for default nodepool to cheapest one
				defaultPool["sku"] = "DEV1-S"
				// Get Scaleway zone for later verification
				if zone, ok := defaultPool["zone"].(string); ok {
					scalewayDefaultZone = zone
				}
			}
		}
		// Set SKU for control plane to cheapest one
		if controlplane, ok := kommoditySection["controlplane"].(map[string]any); ok {
			controlplane["sku"] = "DEV1-S"
		}
		// Set projectID in provider config
		if provider, ok := kommoditySection["provider"].(map[string]any); ok {
			if config, ok := provider["config"].(map[string]any); ok {
				config["projectID"] = scalewayProjectID
			}
		}

		// Unset nodeCIDR to enable public IPv4
		if network, ok := kommoditySection["network"].(map[string]any); ok {
			if ipv4, ok := network["ipv4"].(map[string]any); ok {
				ipv4["nodeCIDR"] = nil
			}
		}
	}

	require.NotEmpty(t, scalewayDefaultZone, "kommodity.nodepools.default.zone must be set in %s", valuesFile)

	installer := action.NewInstall(cfg)
	installer.ReleaseName = releaseName
	installer.Namespace = namespace
	installer.Wait = false

	_, err = installer.Run(chart, values)
	require.NoError(t, err)

	return scalewayDefaultZone
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
