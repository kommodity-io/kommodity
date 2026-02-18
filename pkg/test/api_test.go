package integration_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//nolint:gochecknoglobals // Test environment needs to be reused by all tests.
var env helpers.TestEnvironment

const (
	defaultClusterName     = "ci-test-cluster"
	kommodityLogFile       = "kommodity_container.log"
	virtualMachineGroup    = "kubevirt.io"
	virtualMachineVersion  = "v1"
	virtualMachineResource = "virtualmachines"
	machineGroup           = "cluster.x-k8s.io"
	machineVersion         = "v1beta1"
	machineResource        = "machines"
)

func TestMain(m *testing.M) {
	// --- Setup ---
	var err error

	env, err = helpers.SetupContainers()
	if err != nil {
		log.Println("Failed to set up test containers:", err)
		env.Teardown()
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	if code != 0 {
		err := helpers.WriteKommodityLogsToFile(env.Kommodity, kommodityLogFile)
		if err != nil {
			log.Printf("Failed to write Kommodity container logs to file: %v", err)
		}

		log.Printf("Kommodity container logs written to %s for debugging", kommodityLogFile)
	}

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

//nolint:funlen // Test function length is acceptable.
func TestCreateScalewayCluster(t *testing.T) {
	t.Parallel()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	ctx := context.Background()

	// Create secret that holds Scaleway access and secret key
	scalewayAccessKey := os.Getenv("SCW_ACCESS_KEY")
	scalewaySecretKey := os.Getenv("SCW_SECRET_KEY")
	scalewayDefaultRegion := os.Getenv("SCW_DEFAULT_REGION")
	scalewayProjectID := os.Getenv("SCW_DEFAULT_PROJECT_ID")
	clusterName := os.Getenv("CLUSTER_NAME")

	require.NotEmpty(t, scalewayAccessKey, "SCW_ACCESS_KEY environment variable must be set")
	require.NotEmpty(t, scalewaySecretKey, "SCW_SECRET_KEY environment variable must be set")
	require.NotEmpty(t, scalewayDefaultRegion, "SCW_DEFAULT_REGION environment variable must be set")
	require.NotEmpty(t, scalewayProjectID, "SCW_DEFAULT_PROJECT_ID environment variable must be set")

	if clusterName == "" {
		clusterName = defaultClusterName
	}

	log.Printf("cluster name set to '%s'", clusterName)

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

	log.Printf("Using project ID %s", scalewayProjectID)

	// Install Scaleway cluster helm chart in Kommodity
	scalewayDefaultZone := helpers.InstallKommodityClusterChartScaleway(t, env,
		clusterName, "default", scalewayProjectID)

	// Check that CAPI resources are created in Kommodity
	err = helpers.WaitForK8sResourceCreation(env.KommodityCfg, "default", "worker",
		"cluster.x-k8s.io", "v1beta1", "machines", "", "", 2*time.Minute, 1)
	require.NoError(t, err)

	// Check that Scaleway resources are created
	err = helpers.WaitForScalewayServers(clusterName, scalewayAccessKey,
		scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID, 2, 3*time.Minute)
	require.NoError(t, err)

	// Uninstall cluster chart
	log.Println("Uninstalling kommodity-cluster helm chart...")
	helpers.UninstallKommodityClusterChart(t, env, clusterName, "default")

	// Check that Scaleway resources are deleted
	err = helpers.WaitForScalewayServersDeletion(clusterName, scalewayAccessKey,
		scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID, 3*time.Minute)
	require.NoError(t, err)

	err = helpers.WaitForK8sResourceDeletion(env.KommodityCfg, "default", clusterName,
		"cluster.x-k8s.io", "v1beta1", "clusters", "", "", 2*time.Minute)
	require.NoError(t, err)
}

func TestCreateKubevirtCluster(t *testing.T) {
	t.Parallel()

	clusterName := "kubevirt-test-cluster"
	expectedVMCount := 2 // 1 control plane + 1 worker

	// Setup KubeVirt infrastructure (kind + KubeVirt + CDI)
	infraEnv, err := helpers.SetupKubevirtInfraCluster()
	require.NoError(t, err)

	defer func() {
		teardownErr := helpers.TeardownKubevirtInfraCluster()
		if teardownErr != nil {
			log.Printf("Failed to teardown KubeVirt infra cluster: %v", teardownErr)
		}
	}()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	ctx := context.Background()

	// Create kubevirt-credentials secret in Kommodity with the container-accessible kubeconfig
	_, err = client.CoreV1().Secrets("default").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-credentials",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": infraEnv.Kubeconfig,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	log.Printf("Created kubevirt-credentials secret in Kommodity")

	// Install kommodity-cluster chart with KubeVirt values
	helpers.InstallKommodityClusterChartKubevirt(t, env,
		clusterName, "default", helpers.InfraClusterNamespace)

	// Wait for CAPI resources to be created in Kommodity
	err = helpers.WaitForK8sResourceCreation(env.KommodityCfg, "default", "worker",
		machineGroup, machineVersion, machineResource, "", "", 3*time.Minute, 1)
	require.NoError(t, err)

	// Wait for VirtualMachine CRs to be created in the kind cluster
	err = helpers.WaitForK8sResourceCreation(
		infraEnv.Config, helpers.InfraClusterNamespace, clusterName,
		virtualMachineGroup, virtualMachineVersion, virtualMachineResource,
		"", "", 3*time.Minute, expectedVMCount,
	)
	require.NoError(t, err)

	// Uninstall cluster chart
	log.Println("Uninstalling kommodity-cluster helm chart (KubeVirt)...")
	helpers.UninstallKommodityClusterChart(t, env, clusterName, "default")

	// Note: VM and CAPI resource cleanup verification is intentionally skipped.
	// In emulation mode, VMs never boot, causing CAPI to aggressively create
	// replacement machines. This makes the cascade deletion slow and unreliable.
	// The kind cluster teardown (defer TeardownKubevirtInfraCluster) handles all
	// infrastructure cleanup by deleting the entire kind cluster.
}
