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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
)

const (
	manifestFetchTimeout = 2 * time.Minute
	applyRetryInterval   = 2 * time.Second
	applyRetryTimeout    = 30 * time.Second
	fieldManager         = "kommodity-test"
	yamlDecoderBuffer    = 4096
)

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

// WaitForK8sResourceCreation waits for at least minCount Kubernetes resources to be created
// that match the given criteria.
func WaitForK8sResourceCreation(
	config *rest.Config,
	namespace string,
	nameContains string,
	group string,
	version string,
	kind string,
	fieldPath string,
	fieldValue string,
	timeout time.Duration,
	minCount int,
) error {
	return waitForK8sResource(config, namespace, nameContains, group, version, kind,
		fieldPath, fieldValue, timeout, minCount, true)
}

// WaitForK8sResourceDeletion waits for a Kubernetes resource to be deleted that matches the given criteria.
func WaitForK8sResourceDeletion(
	config *rest.Config,
	namespace string,
	nameContains string,
	group string,
	version string,
	kind string,
	fieldPath string,
	fieldValue string,
	timeout time.Duration,
) error {
	return waitForK8sResource(config, namespace, nameContains, group, version, kind,
		fieldPath, fieldValue, timeout, 0, false)
}

//nolint:funlen // Length is driven by logging and error formatting across creation/deletion paths.
func waitForK8sResource(
	config *rest.Config,
	namespace string,
	nameContains string,
	group string,
	version string,
	kind string,
	fieldPath string,
	fieldValue string,
	timeout time.Duration,
	minCount int,
	waitForExistence bool,
) error {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: kind,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	action := "creation"
	if !waitForExistence {
		action = "deletion"
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		count, countErr := countMatchingResources(ctx, client, gvr, namespace, nameContains, fieldPath, fieldValue)
		if countErr != nil {
			return false, countErr
		}

		if waitForExistence {
			if count >= minCount {
				log.Printf("Found %d resource(s) %s/%s/%s in namespace %s (name contains: %q, field %q=%q, needed %d)",
					count, group, version, kind, namespace, nameContains, fieldPath, fieldValue, minCount)

				return true, nil
			}

			log.Printf("Waiting for %s of resource %s/%s/%s in namespace %s (name contains: %q, field %q=%q, found %d, need %d)",
				action, group, version, kind, namespace, nameContains, fieldPath, fieldValue, count, minCount)

			return false, nil
		}

		if count == 0 {
			return true, nil
		}

		log.Printf("Waiting for %s of resource %s/%s/%s in namespace %s (name contains: %q, field %q=%q, still %d remaining)",
			action, group, version, kind, namespace, nameContains, fieldPath, fieldValue, count)

		return false, nil
	})
	if err != nil {
		if waitForExistence {
			return fmt.Errorf("resource %s/%s/%s not found in namespace %s within timeout (name contains: %q, field %q=%q): %w",
				group, version, kind, namespace, nameContains, fieldPath, fieldValue, err)
		}

		return fmt.Errorf("resource %s/%s/%s still exists in namespace %s after timeout (name contains: %q, field %q=%q): %w",
			group, version, kind, namespace, nameContains, fieldPath, fieldValue, err)
	}

	result := "found"
	if !waitForExistence {
		result = "deleted"
	}

	log.Printf("Resource %s/%s/%s %s in namespace %s (name contains: %q, field %q=%q)",
		group, version, kind, result, namespace, nameContains, fieldPath, fieldValue)

	return nil
}

//nolint:cyclop // Function complexity is acceptable for this utility.
func countMatchingResources(
	ctx context.Context,
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace string,
	nameContains string,
	fieldPath string,
	fieldValue string,
) (int, error) {
	var lister dynamic.ResourceInterface
	if namespace == "" {
		lister = client.Resource(gvr)
	} else {
		lister = client.Resource(gvr).Namespace(namespace)
	}

	list, err := lister.List(ctx, metav1.ListOptions{})
	if err != nil {
		// Treat "not found" as zero resources — the CRD may not be registered yet.
		if apierrors.IsNotFound(err) {
			return 0, nil
		}

		return 0, fmt.Errorf("failed to list resources: %w", err)
	}

	count := 0

	for _, item := range list.Items {
		if nameContains != "" && !strings.Contains(item.GetName(), nameContains) {
			continue
		}

		if fieldPath != "" {
			parts := strings.Split(fieldPath, ".")

			value, found, err := unstructured.NestedString(item.Object, parts...)
			if err != nil || !found || value != fieldValue {
				continue
			}
		}

		if fieldValue != "" && fieldPath == "" {
			continue
		}

		count++
	}

	return count, nil
}
