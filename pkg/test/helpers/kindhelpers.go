package helpers

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
)

const (
	kindClusterName          = "kommodity-kubevirt-test"
	kubevirtVersion          = "v1.4.0"
	cdiVersion               = "v1.61.0"
	kubevirtReadyTimeout     = 5 * time.Minute
	cdiReadyTimeout          = 5 * time.Minute
	instanceTypeSKU          = "s1.medium"
	instanceTypeCPU          = 2
	instanceTypeMemory       = "4Gi"
	kubevirtNamespace        = "kubevirt"
	tempDirName              = "kommodity-test"
	kubeconfigFilePermission = 0o600
	kubeconfigDirPermission  = 0o750
	urlParts                 = 3
)

// InfraClusterNamespace is the namespace in the kind cluster where KubeVirt VMs are deployed.
const InfraClusterNamespace = "kubevirt-test-ns"

// KubevirtInfraEnv holds the configuration for the KubeVirt infrastructure cluster.
type KubevirtInfraEnv struct {
	Config     *rest.Config // Host-accessible config for the kind cluster
	Kubeconfig []byte       // Container-accessible kubeconfig (for the credential secret)
}

// SetupKubevirtInfraCluster orchestrates the full KubeVirt infrastructure setup:
// kind cluster creation, KubeVirt + CDI installation, instance type creation, and namespace creation.
func SetupKubevirtInfraCluster() (KubevirtInfraEnv, error) {
	config, apiServerPort, err := createKindCluster()
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = installKubeVirt(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = installCDI(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = waitForKubeVirtReady(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = waitForCDIReady(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = createInstanceTypes(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	err = createInfraNamespace(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	kubeconfig, err := buildContainerAccessibleKubeconfig(apiServerPort)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("setup kubevirt infra: %w", err)
	}

	return KubevirtInfraEnv{
		Config:     config,
		Kubeconfig: kubeconfig,
	}, nil
}

// TeardownKubevirtInfraCluster deletes the kind cluster used for KubeVirt testing.
func TeardownKubevirtInfraCluster() error {
	err := deleteKindCluster()
	if err != nil {
		return fmt.Errorf("teardown kubevirt infra: %w", err)
	}

	return nil
}

// createKindCluster creates a kind cluster and returns the REST config and the API server port.
func createKindCluster() (*rest.Config, string, error) {
	provider := cluster.NewProvider()

	log.Printf("Creating kind cluster %q...", kindClusterName)

	err := provider.Create(kindClusterName)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", errKindClusterCreation, err)
	}

	log.Printf("Kind cluster %q created successfully", kindClusterName)

	kubeconfigStr, err := provider.KubeConfig(kindClusterName, false)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get kubeconfig for kind cluster: %w", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigStr))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	// Extract the API server port from the external kubeconfig.
	// The kind cluster exposes the API server on a random host port.
	externalPort, err := extractAPIServerPort(kubeconfigStr)
	if err != nil {
		return nil, "", fmt.Errorf("failed to extract API server port: %w", err)
	}

	log.Printf("Kind cluster API server external port: %s", externalPort)

	return config, externalPort, nil
}

// extractAPIServerPort extracts the port from a kubeconfig server URL.
func extractAPIServerPort(kubeconfigStr string) (string, error) {
	cfg, err := clientcmd.Load([]byte(kubeconfigStr))
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	for _, clusterInfo := range cfg.Clusters {
		server := clusterInfo.Server
		// Server is typically https://127.0.0.1:PORT
		parts := strings.Split(server, ":")
		if len(parts) >= urlParts {
			return parts[len(parts)-1], nil
		}
	}

	return "", fmt.Errorf("%w: no cluster found in kubeconfig", errKindClusterCreation)
}

// deleteKindCluster deletes the kind cluster by name.
func deleteKindCluster() error {
	provider := cluster.NewProvider()

	log.Printf("Deleting kind cluster %q...", kindClusterName)

	err := provider.Delete(kindClusterName, "")
	if err != nil {
		return fmt.Errorf("%w: %w", errKindClusterCreation, err)
	}

	log.Printf("Kind cluster %q deleted", kindClusterName)

	return nil
}

// buildContainerAccessibleKubeconfig builds a kubeconfig that uses host.docker.internal
// so containers running in Docker can reach the kind cluster's API server.
func buildContainerAccessibleKubeconfig(apiServerPort string) ([]byte, error) {
	provider := cluster.NewProvider()

	kubeconfigStr, err := provider.KubeConfig(kindClusterName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig for container-accessible config: %w", err)
	}

	cfg, err := clientcmd.Load([]byte(kubeconfigStr))
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Modify the cluster server to use host.docker.internal and skip TLS verification
	for _, clusterInfo := range cfg.Clusters {
		clusterInfo.Server = "https://host.docker.internal:" + apiServerPort
		clusterInfo.InsecureSkipTLSVerify = true
		clusterInfo.CertificateAuthorityData = nil
	}

	data, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to write container-accessible kubeconfig: %w", err)
	}

	return data, nil
}

// installKubeVirt installs the KubeVirt operator and CR with emulation mode enabled.
func installKubeVirt(config *rest.Config) error {
	log.Printf("Installing KubeVirt %s...", kubevirtVersion)

	kubeconfigPath, err := getKindKubeconfigPath()
	if err != nil {
		return fmt.Errorf("%w: %w", errKubeVirtInstall, err)
	}

	operatorURL := fmt.Sprintf(
		"https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-operator.yaml",
		kubevirtVersion,
	)

	err = kubectlApply(kubeconfigPath, operatorURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply operator: %w", errKubeVirtInstall, err)
	}

	crURL := fmt.Sprintf(
		"https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-cr.yaml",
		kubevirtVersion,
	)

	err = kubectlApply(kubeconfigPath, crURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply CR: %w", errKubeVirtInstall, err)
	}

	err = patchKubeVirtEmulation(config)
	if err != nil {
		return fmt.Errorf("%w: failed to enable emulation: %w", errKubeVirtInstall, err)
	}

	log.Println("KubeVirt installed successfully")

	return nil
}

// installCDI installs the CDI operator and CR.
func installCDI(_ *rest.Config) error {
	log.Printf("Installing CDI %s...", cdiVersion)

	kubeconfigPath, err := getKindKubeconfigPath()
	if err != nil {
		return fmt.Errorf("%w: %w", errCDIInstall, err)
	}

	operatorURL := fmt.Sprintf(
		"https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-operator.yaml",
		cdiVersion,
	)

	err = kubectlApply(kubeconfigPath, operatorURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply operator: %w", errCDIInstall, err)
	}

	crURL := fmt.Sprintf(
		"https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-cr.yaml",
		cdiVersion,
	)

	err = kubectlApply(kubeconfigPath, crURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply CR: %w", errCDIInstall, err)
	}

	log.Println("CDI installed successfully")

	return nil
}

// waitForKubeVirtReady polls until the KubeVirt CR reaches the "Deployed" phase.
func waitForKubeVirtReady(config *rest.Config) error {
	log.Println("Waiting for KubeVirt to be ready...")

	err := WaitForK8sResourceCreation(
		config, kubevirtNamespace, "kubevirt",
		"kubevirt.io", "v1", "kubevirts",
		"status.phase", "Deployed", kubevirtReadyTimeout, 1,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errKubeVirtNotReady, err)
	}

	log.Println("KubeVirt is ready")

	return nil
}

// waitForCDIReady polls until the CDI CR reaches the "Deployed" phase.
func waitForCDIReady(config *rest.Config) error {
	log.Println("Waiting for CDI to be ready...")

	err := WaitForK8sResourceCreation(
		config, "", "cdi",
		"cdi.kubevirt.io", "v1beta1", "cdis",
		"status.phase", "Deployed", cdiReadyTimeout, 1,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errCDINotReady, err)
	}

	log.Println("CDI is ready")

	return nil
}

// createInstanceTypes creates VirtualMachineClusterInstancetype resources needed by the Helm chart.
func createInstanceTypes(config *rest.Config) error {
	log.Printf("Creating VirtualMachineClusterInstancetype %q...", instanceTypeSKU)

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "instancetype.kubevirt.io",
		Version:  "v1beta1",
		Resource: "virtualmachineclusterinstancetypes",
	}

	instanceType := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "instancetype.kubevirt.io/v1beta1",
			"kind":       "VirtualMachineClusterInstancetype",
			"metadata": map[string]any{
				"name": instanceTypeSKU,
			},
			"spec": map[string]any{
				"cpu": map[string]any{
					"guest": int64(instanceTypeCPU),
				},
				"memory": map[string]any{
					"guest": instanceTypeMemory,
				},
			},
		},
	}

	ctx := context.Background()

	_, err = dynClient.Resource(gvr).Create(ctx, instanceType, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create instance type %q: %w", instanceTypeSKU, err)
	}

	log.Printf("VirtualMachineClusterInstancetype %q created", instanceTypeSKU)

	return nil
}

// createInfraNamespace creates the namespace in the kind cluster where VMs will be deployed.
func createInfraNamespace(config *rest.Config) error {
	log.Printf("Creating namespace %q...", InfraClusterNamespace)

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	ctx := context.Background()

	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: InfraClusterNamespace,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace %q: %w", InfraClusterNamespace, err)
	}

	log.Printf("Namespace %q created", InfraClusterNamespace)

	return nil
}

// patchKubeVirtEmulation patches the KubeVirt CR to enable software emulation.
func patchKubeVirtEmulation(config *rest.Config) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "kubevirts",
	}

	ctx := context.Background()

	kubevirtCR, err := dynClient.Resource(gvr).Namespace(kubevirtNamespace).Get(ctx, "kubevirt", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get KubeVirt CR: %w", err)
	}

	err = unstructured.SetNestedField(
		kubevirtCR.Object,
		true,
		"spec", "configuration", "developerConfiguration", "useEmulation",
	)
	if err != nil {
		return fmt.Errorf("failed to set useEmulation field: %w", err)
	}

	_, err = dynClient.Resource(gvr).Namespace(kubevirtNamespace).Update(ctx, kubevirtCR, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update KubeVirt CR: %w", err)
	}

	log.Println("KubeVirt emulation mode enabled")

	return nil
}

// getKindKubeconfigPath writes the kind cluster kubeconfig to a temp file and returns the path.
func getKindKubeconfigPath() (string, error) {
	provider := cluster.NewProvider()

	kubeconfigStr, err := provider.KubeConfig(kindClusterName, false)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	tmpDir := filepath.Join(os.TempDir(), tempDirName)

	err = os.MkdirAll(tmpDir, kubeconfigDirPermission)
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir %s: %w", tmpDir, err)
	}

	tmpFile := filepath.Join(tmpDir, "kind-kubeconfig-"+kindClusterName)

	err = os.WriteFile(tmpFile, []byte(kubeconfigStr), kubeconfigFilePermission)
	if err != nil {
		return "", fmt.Errorf("failed to write kubeconfig to %s: %w", tmpFile, err)
	}

	return tmpFile, nil
}

// kubectlApply runs kubectl apply -f <url> with the specified kubeconfig.
func kubectlApply(kubeconfigPath string, manifestURL string) error {
	log.Printf("Running kubectl apply -f %s", manifestURL)

	ctx, cancel := context.WithTimeout(context.Background(), kubevirtReadyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", manifestURL, "--kubeconfig", kubeconfigPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %s: %w", string(output), err)
	}

	log.Printf("kubectl apply output: %s", string(output))

	return nil
}
