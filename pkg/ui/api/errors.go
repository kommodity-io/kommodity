package api

import "errors"

var (
	// ErrOIDCNotConfigured is returned when OIDC is not configured on the cluster.
	ErrOIDCNotConfigured = errors.New("OIDC is not configured on the cluster")
	// ErrNoMachineConfigSecret is returned when no machine config secret is found for the cluster.
	ErrNoMachineConfigSecret = errors.New("no machine config secret found for cluster")
	// ErrNoControlPlaneBootstrapData is returned when no control plane bootstrap data secret is found for the cluster.
	ErrNoControlPlaneBootstrapData = errors.New("no control plane bootstrap data secret found for cluster")
	// ErrKubeConfigSecretIsEmpty is returned when the kubeconfig secret is empty.
	ErrKubeConfigSecretIsEmpty = errors.New("kubeconfig secret is empty")
	// ErrNoHelmReleaseSecret is returned when no Helm release secret is found for the cluster.
	ErrNoHelmReleaseSecret = errors.New("no helm release secret found")
	// ErrReleaseDataNotFound is returned when release data is not found in the Helm secret.
	ErrReleaseDataNotFound = errors.New("release data not found in secret")
)

const (
	// UnknownVersion is the default value when a version cannot be determined.
	UnknownVersion = "Unknown"
)
