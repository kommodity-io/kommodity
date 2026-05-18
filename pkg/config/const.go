package config

const (
	// KommodityNamespace is the namespace where shared Kommodity components operate.
	// Per-cluster resources live in a namespace named after the cluster.
	KommodityNamespace = "kommodity-system"
	// ManagedByLabel is the label key used to indicate resources managed by Kommodity.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value used for the ManagedByLabel.
	ManagedByValue = "kommodity"
	// DeploymentNameLabel is the label key used to indicate the deployment name.
	DeploymentNameLabel = "cluster.x-k8s.io/deployment-name"
	// ClusterNameLabel is the standard CAPI label key indicating the owning cluster.
	ClusterNameLabel = "cluster.x-k8s.io/cluster-name"
	// NodeUUIDLabel is the label key used to record a Talos node's UUID on managed resources.
	NodeUUIDLabel = "talos.dev/node-uuid"
	// NodeIPLabel is the label key used to record a Talos node's IP on managed resources.
	NodeIPLabel = "talos.dev/node-ip"
)

// GetKommodityLabels returns the standard labels for Kommodity-managed resources.
func GetKommodityLabels(nodeUUID string, nodeIP string) map[string]string {
	return map[string]string{
		ManagedByLabel: ManagedByValue,
		NodeUUIDLabel:  nodeUUID,
		NodeIPLabel:    nodeIP,
	}
}

// GetKommodityClusterLabels returns the standard labels plus the owning cluster name.
func GetKommodityClusterLabels(nodeUUID string, nodeIP string, clusterName string) map[string]string {
	labels := GetKommodityLabels(nodeUUID, nodeIP)
	labels[ClusterNameLabel] = clusterName

	return labels
}
