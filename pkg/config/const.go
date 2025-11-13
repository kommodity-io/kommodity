package config

const (
	// KommodityNamespace is the namespace where Kommodity components operate.
	KommodityNamespace = "kommodity-system"
	// ManagedByLabel is the label key used to indicate resources managed by Kommodity.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// DeploymentNameLabel is the label key used to indicate the deployment name.
	DeploymentNameLabel = "cluster.x-k8s.io/deployment-name"
)

// GetKommodityLabels returns the standard labels for Kommodity-managed resources.
func GetKommodityLabels(nodeUUID, nodeIP string) map[string]string {
	return map[string]string{
		ManagedByLabel:        "kommodity",
		"talos.dev/node-uuid": nodeUUID,
		"talos.dev/node-ip":   nodeIP,
	}
}
