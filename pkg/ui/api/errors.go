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
)
