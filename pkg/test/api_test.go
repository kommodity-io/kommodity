package test_test

import (
	"context"
	"encoding/base64"
	"net"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
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

	client, err := helpers.KommodityClient(env.KommodityCfg)
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

func TestCreateKubevirtCluster(t *testing.T) {
	t.Parallel()

	client, err := helpers.KommodityClient(env.KommodityCfg)
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
	waitForResource(t, "default", "cluster.x-k8s.io", "v1beta1", "machines", 2*time.Minute)
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

func waitForResource(t *testing.T, namespace string, group string, version string, resource string, timeout time.Duration) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		return len(list.Items) > 0, nil
	})
	require.NoError(t, err)
}
