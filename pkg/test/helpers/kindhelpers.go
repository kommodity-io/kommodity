package helpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
)

const (
	kindClusterName      = "kommodity-kubevirt-test"
	kubevirtVersion      = "v1.4.0"
	cdiVersion           = "v1.61.0"
	kubevirtReadyTimeout = 5 * time.Minute
	cdiReadyTimeout      = 5 * time.Minute
	instanceTypeSKU      = "s1.medium"
	instanceTypeCPU      = 2
	instanceTypeMemory   = "4Gi"
	kubevirtNamespace    = "kubevirt"
	manifestFetchTimeout = 2 * time.Minute
	applyRetryInterval   = 2 * time.Second
	applyRetryTimeout    = 30 * time.Second
	fieldManager         = "kommodity-test"
	yamlDecoderBuffer    = 4096
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

	// Bind the API server to 0.0.0.0 so it's reachable from Docker containers
	// via the bridge network (required on Linux/CI where host.docker.internal
	// resolves to the Docker gateway IP, not localhost).
	kindConfig := []byte(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "0.0.0.0"
`)

	err := provider.Create(kindClusterName, cluster.CreateWithRawConfig(kindConfig))
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
		_, port, err := net.SplitHostPort(strings.TrimPrefix(server, "https://"))
		if err != nil {
			return "", fmt.Errorf("failed to parse server URL: %w", err)
		}

		return port, nil
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

// applyManifestURL fetches a YAML manifest from a URL and applies all resources to the cluster.
func applyManifestURL(ctx context.Context, config *rest.Config, manifestURL string) error {
	log.Printf("Applying manifest from %s", manifestURL)

	objects, err := fetchAndDecodeManifest(ctx, manifestURL)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest from %s: %w", manifestURL, err)
	}

	err = applyObjects(ctx, config, objects)
	if err != nil {
		return fmt.Errorf("failed to apply manifest from %s: %w", manifestURL, err)
	}

	log.Printf("Successfully applied %d resources from %s", len(objects), manifestURL)

	return nil
}

// fetchAndDecodeManifest fetches a YAML manifest from a URL and decodes it into unstructured objects.
func fetchAndDecodeManifest(ctx context.Context, manifestURL string) ([]*unstructured.Unstructured, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, manifestFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", errManifestFetch, resp.StatusCode)
	}

	return decodeMultiDocYAML(resp.Body)
}

// decodeMultiDocYAML decodes a multi-document YAML stream into unstructured Kubernetes objects.
func decodeMultiDocYAML(reader io.Reader) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	decoder := utilyaml.NewYAMLOrJSONDecoder(reader, yamlDecoderBuffer)

	for {
		obj := &unstructured.Unstructured{}

		err := decoder.Decode(obj)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("failed to decode YAML document: %w", err)
		}

		// Skip empty documents (e.g. bare "---" separators).
		if obj.GetKind() == "" {
			continue
		}

		objects = append(objects, obj)
	}

	return objects, nil
}

// applyObjects applies a list of unstructured objects, processing CRDs first to ensure
// custom resource types are registered before their instances are applied.
func applyObjects(
	ctx context.Context,
	config *rest.Config,
	objects []*unstructured.Unstructured,
) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(discoveryClient),
	)

	var crds, others []*unstructured.Unstructured

	for _, obj := range objects {
		if obj.GetKind() == "CustomResourceDefinition" {
			crds = append(crds, obj)
		} else {
			others = append(others, obj)
		}
	}

	for _, obj := range crds {
		err := serverSideApply(ctx, dynClient, mapper, obj)
		if err != nil {
			return fmt.Errorf("failed to apply CRD %q: %w", obj.GetName(), err)
		}
	}

	// Reset mapper cache so newly registered CRDs are discoverable.
	if len(crds) > 0 {
		mapper.Reset()
	}

	for _, obj := range others {
		err := serverSideApplyWithRetry(ctx, dynClient, mapper, obj)
		if err != nil {
			return fmt.Errorf("failed to apply %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	return nil
}

// serverSideApply applies a single unstructured object using server-side apply.
func serverSideApply(
	ctx context.Context,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	obj *unstructured.Unstructured,
) error {
	gvk := obj.GroupVersionKind()

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to find REST mapping for %s: %w", gvk, err)
	}

	var resource dynamic.ResourceInterface
	if mapping.Scope.Name() == apimeta.RESTScopeNameNamespace {
		resource = client.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		resource = client.Resource(mapping.Resource)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}

	_, err = resource.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("failed to apply %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}

	return nil
}

// serverSideApplyWithRetry retries server-side apply when the resource type
// is not yet registered (e.g. CRD propagation delay).
func serverSideApplyWithRetry(
	ctx context.Context,
	client dynamic.Interface,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	obj *unstructured.Unstructured,
) error {
	deadline := time.Now().Add(applyRetryTimeout)

	for {
		err := serverSideApply(ctx, client, mapper, obj)
		if err == nil {
			return nil
		}

		if !apimeta.IsNoMatchError(err) || time.Now().After(deadline) {
			return err
		}

		log.Printf("Resource type not yet registered for %s %q, retrying...", obj.GetKind(), obj.GetName())
		mapper.Reset()
		time.Sleep(applyRetryInterval)
	}
}
