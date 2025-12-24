package test_test

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

//nolint:gochecknoglobals // Test environment needs to be reused by all tests.
var env helpers.TestEnvironment

func TestMain(m *testing.M) {
	// --- Setup ---
	env = helpers.SetupContainers()

	// Run tests
	code := m.Run()

	// --- Teardown ---
	env.Teardown()

	os.Exit(code)
}

func TestAPIIntegration(t *testing.T) {
	t.Parallel()

	client := env.KommodityK8s
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

func TestCreateKubevirtCluster(t *testing.T) {
	t.Parallel()

	client := env.KommodityK8s
	ctx := context.Background()

	// Ensure default namespace exists

	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	// Create secret that holds K3s kubeconfig

	encodedKubeconfig := base64.StdEncoding.EncodeToString(env.K3sKubeconfig)
	secret, err := client.CoreV1().Secrets("default").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-credentials",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": []byte(encodedKubeconfig),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeOpaque, secret.Type)

	created, err := client.CoreV1().Secrets("default").Get(ctx, "kubevirt-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeOpaque, created.Type)
	require.Equal(t, []byte(encodedKubeconfig), created.Data["kubeconfig"])

	// Install Kubevirt cluster helm chart in Kommodity
	installKommodityClusterChart(t, "kubevirt-cluster", "default")

	// Check that CAPI resources are created in Kommodity
	
}

func installKommodityClusterChart(t *testing.T, releaseName string, namespace string) {
	t.Helper()

	repoRoot := helpers.RepoRoot()
	chartPath := filepath.Join(repoRoot, "charts", "kommodity-cluster")
	valuesPath := filepath.Join(repoRoot, "charts", "kommodity-cluster", "values.kubevirt.yaml")

	cfg := new(action.Configuration)
	restGetter := genericclioptions.NewConfigFlags(false)
	apiServer := "http://" + net.JoinHostPort(env.AppHost, env.AppPort)
	restGetter.APIServer = &apiServer
	restGetter.Namespace = &namespace

	err := cfg.Init(restGetter, namespace, "secret", func(string, ...interface{}) {})
	require.NoError(t, err)

	chart, err := loader.Load(chartPath)
	require.NoError(t, err)

	values, err := chartutil.ReadValuesFile(valuesPath)
	require.NoError(t, err)

	installer := action.NewInstall(cfg)
	installer.ReleaseName = releaseName
	installer.Namespace = namespace
	installer.Wait = false

	_, err = installer.Run(chart, values)
	require.NoError(t, err)
}
