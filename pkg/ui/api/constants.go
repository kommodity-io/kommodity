package api

const (
	// UnknownVersion is the default value when a version cannot be determined.
	UnknownVersion = "Unknown"

	// MachinePhaseRunning is the phase value for running machines.
	MachinePhaseRunning = "Running"
	// MachinePhaseProvisioning is the phase value for provisioning machines.
	MachinePhaseProvisioning = "Provisioning"

	// HelmLabelOwner is the Helm label key for the owner field.
	HelmLabelOwner = "owner"
	// HelmLabelName is the Helm label key for the release name.
	HelmLabelName = "name"
	// HelmLabelStatus is the Helm label key for the release status.
	HelmLabelStatus = "status"
	// HelmOwnerHelm is the Helm label value indicating Helm ownership.
	HelmOwnerHelm = "helm"
	// HelmStatusDeployed is the Helm label value for deployed releases.
	HelmStatusDeployed = "deployed"

	// HelmSecretKeyRelease is the key name for release data in Helm secrets.
	HelmSecretKeyRelease = "release"

	// ClusterNameLabel is the Cluster API label key for cluster name.
	ClusterNameLabel = "cluster.x-k8s.io/cluster-name"

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// HealthCheckTimeoutSeconds is the timeout duration for cluster health checks.
	HealthCheckTimeoutSeconds = 5
)
