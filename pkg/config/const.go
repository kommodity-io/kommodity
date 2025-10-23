package config

const (
	// KommodityNamespace is the namespace where Kommodity components operate.
	KommodityNamespace = "kommodity-system"
)

// GetKommodityLabels returns the standard labels for Kommodity-managed resources.
func GetKommodityLabels(nodeUUID, nodeIP string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "kommodity",
		"talos.dev/node-uuid":          nodeUUID,
		"talos.dev/node-ip":            nodeIP,
	}
}
