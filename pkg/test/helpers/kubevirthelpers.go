package helpers

import (
	"context"
	"fmt"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// InfraClusterNamespace is the namespace in the kind cluster where KubeVirt VMs are deployed.
const (
	kubevirtVersion      = "v1.4.0"
	cdiVersion           = "v1.61.0"
	kubevirtReadyTimeout = 5 * time.Minute
	cdiReadyTimeout      = 5 * time.Minute
	instanceTypeSKU      = "s1.medium"
	instanceTypeCPU      = 2
	instanceTypeMemory   = "4Gi"
	kubevirtNamespace    = "kubevirt"
	kubevirtValuesFile   = "values.kubevirt.yaml"
)

// KubevirtInfraEnv holds the configuration for the KubeVirt infrastructure cluster.
type KubevirtInfraEnv struct {
	Config     *rest.Config // Host-accessible config for the kind cluster
	Kubeconfig []byte       // Container-accessible kubeconfig (for the credential secret)
}

// KubevirtInfra holds KubeVirt-specific configuration for chart installation.
type KubevirtInfra struct {
	InfraClusterNamespace    string
	ControlPlaneEndpointHost string
	ControlPlaneEndpointPort int64
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
		"kommodity.controlplane.replicas":      int64(1),
		"kommodity.nodepools.default.replicas": int64(1),
	}
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
		return KubevirtInfraEnv{}, fmt.Errorf("failed to install kubevirt: %w", err)
	}

	err = installCDI(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to install CDI: %w", err)
	}

	err = waitForKubeVirtReady(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to wait for kubevirt ready: %w", err)
	}

	err = waitForCDIReady(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to wait for CDI ready: %w", err)
	}

	err = createInstanceTypes(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to create instance types: %w", err)
	}

	err = createInfraNamespace(config)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to create infra namespace: %w", err)
	}

	kubeconfig, err := buildContainerAccessibleKubeconfig(apiServerPort)
	if err != nil {
		return KubevirtInfraEnv{}, fmt.Errorf("failed to build container-accessible kubeconfig: %w", err)
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
		return fmt.Errorf("failed to teardown kubevirt infra: %w", err)
	}

	return nil
}

// installKubeVirt installs the KubeVirt operator and CR with emulation mode enabled.
func installKubeVirt(config *rest.Config) error {
	log.Printf("Installing KubeVirt %s...", kubevirtVersion)

	ctx := context.Background()

	operatorURL := fmt.Sprintf(
		"https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-operator.yaml",
		kubevirtVersion,
	)

	err := applyManifestURL(ctx, config, operatorURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply operator: %w", errKubeVirtInstall, err)
	}

	crURL := fmt.Sprintf(
		"https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-cr.yaml",
		kubevirtVersion,
	)

	err = applyManifestURL(ctx, config, crURL)
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
func installCDI(config *rest.Config) error {
	log.Printf("Installing CDI %s...", cdiVersion)

	ctx := context.Background()

	operatorURL := fmt.Sprintf(
		"https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-operator.yaml",
		cdiVersion,
	)

	err := applyManifestURL(ctx, config, operatorURL)
	if err != nil {
		return fmt.Errorf("%w: failed to apply operator: %w", errCDIInstall, err)
	}

	crURL := fmt.Sprintf(
		"https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-cr.yaml",
		cdiVersion,
	)

	err = applyManifestURL(ctx, config, crURL)
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
