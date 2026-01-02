package test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

//nolint:gochecknoglobals // Test environment needs to be reused by all tests.
var env helpers.TestEnvironment

func TestMain(m *testing.M) {
	// --- Setup ---
	var err error
	env, err = helpers.SetupContainers()
	if err != nil {
		fmt.Println("Failed to set up test containers:", err)
		env.Teardown()
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// --- Teardown ---
	env.Teardown()

	os.Exit(code)
}

func TestAPIIntegration(t *testing.T) {
	t.Parallel()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)
	groups, err := client.Discovery().ServerGroups()
	require.NoError(t, err)

	var coreGroupVersions []string
	for _, group := range groups.Groups {
		if group.Name == "" {
			for _, version := range group.Versions {
				coreGroupVersions = append(coreGroupVersions, version.Version)
			}
			break
		}
	}
	require.Contains(t, coreGroupVersions, "v1")
}

func TestCreateScalewayCluster(t *testing.T) {
	t.Parallel()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)
	ctx := context.Background()

	// Ensure default namespace exists

	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	// Create secret that holds Scaleway access and secret key
	scalewayAccessKey := os.Getenv("SCW_ACCESS_KEY")
	scalewaySecretKey := os.Getenv("SCW_SECRET_KEY")
	scalewayDefaultRegion := os.Getenv("SCW_DEFAULT_REGION")
	scalewayProjectID := os.Getenv("SCW_DEFAULT_PROJECT_ID")

	require.NotEmpty(t, scalewayAccessKey, "SCW_ACCESS_KEY environment variable must be set")
	require.NotEmpty(t, scalewaySecretKey, "SCW_SECRET_KEY environment variable must be set")
	require.NotEmpty(t, scalewayDefaultRegion, "SCW_DEFAULT_REGION environment variable must be set")
	require.NotEmpty(t, scalewayProjectID, "SCW_DEFAULT_PROJECT_ID environment variable must be set")

	_, err = client.CoreV1().Secrets("default").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scaleway-secret",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"SCW_ACCESS_KEY":         []byte(scalewayAccessKey),
			"SCW_SECRET_KEY":         []byte(scalewaySecretKey),
			"SCW_DEFAULT_REGION":     []byte(scalewayDefaultRegion),
			"SCW_DEFAULT_PROJECT_ID": []byte(scalewayProjectID),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Install Scaleway cluster helm chart in Kommodity
	scalewayDefaultZone := installKommodityClusterChart(t, "scaleway-cluster", "default", "values.scaleway.yaml", scalewayProjectID)

	// Check that CAPI resources are created in Kommodity
	err = helpers.WaitForK8sResource(env.KommodityCfg, "default", "worker", "cluster.x-k8s.io", "v1beta1", "machines", "", "", 2*time.Minute)
	require.NoError(t, err)

// Check that Scaleway resources are created
	err = helpers.WaitForScalewayServer(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID, 2, 5*time.Minute)
	require.NoError(t, err)

	// Uninstall cluster chart
	log.Println("Uninstalling kommodity-cluster helm chart...")
	uninstallKommodityClusterChart(t, "scaleway-cluster", "default")

	// Check that Scaleway resources are deleted
	err = helpers.WaitForScalewayServersDeletion(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID, 5*time.Minute)
	require.NoError(t, err)

}

func installKommodityClusterChart(t *testing.T, releaseName string, namespace string, valuesFile string, scalewayProjectID string) string {
	t.Helper()

	repoRoot, err := helpers.FindRepoRoot()
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

func uninstallKommodityClusterChart(t *testing.T, releaseName string, namespace string) {
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
