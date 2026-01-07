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
func InstallKommodityClusterChart(t *testing.T, env TestEnvironment, releaseName string, namespace string, valuesFile string, scalewayProjectID string) string {
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

	err = cfg.Init(restGetter, namespace, "secret", func(string, ...interface{}) {})
	require.NoError(t, err)

	chart, err := loader.Load(chartPath)
	require.NoError(t, err)

	values, err := chartutil.ReadValuesFile(valuesPath)
	require.NoError(t, err)

	// Read default zone from values file to reuse in Scaleway verification.
	scalewayDefaultZone := ""
	if kommoditySection, ok := values["kommodity"].(map[string]interface{}); ok {
		if nodepools, ok := kommoditySection["nodepools"].(map[string]interface{}); ok {
			if defaultPool, ok := nodepools["default"].(map[string]interface{}); ok {
				if zone, ok := defaultPool["zone"].(string); ok {
					scalewayDefaultZone = zone
				}
			}
		}
	}
	require.NotEmpty(t, scalewayDefaultZone, "kommodity.nodepools.default.zone must be set in %s", valuesFile)

	// Override projectID with the value provided via environment to avoid hard-coded data in the values file.
	kommodityVals, ok := values["kommodity"].(map[string]interface{})
	if !ok || kommodityVals == nil {
		kommodityVals = map[string]interface{}{}
		values["kommodity"] = kommodityVals
	}
	providerVals, ok := kommodityVals["provider"].(map[string]interface{})
	if !ok || providerVals == nil {
		providerVals = map[string]interface{}{}
		kommodityVals["provider"] = providerVals
	}
	configVals, ok := providerVals["config"].(map[string]interface{})
	if !ok || configVals == nil {
		configVals = map[string]interface{}{}
		providerVals["config"] = configVals
	}
	configVals["projectID"] = scalewayProjectID

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

	err := cfg.Init(restGetter, namespace, "secret", func(string, ...interface{}) {})
	require.NoError(t, err)

	uninstaller := action.NewUninstall(cfg)

	_, err = uninstaller.Run(releaseName)
	require.NoError(t, err)
}
