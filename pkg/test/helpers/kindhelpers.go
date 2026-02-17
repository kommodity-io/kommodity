package helpers

import (
	"fmt"
	"log"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
)

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
