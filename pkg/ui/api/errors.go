package api

import "errors"

var (
	// ErrFailedToFindContext is returned when the context for a cluster is not found.
	ErrFailedToFindContext = errors.New("failed to find context for cluster")
	// ErrOIDCNotConfigured is returned when OIDC is not configured on the cluster.
	ErrOIDCNotConfigured = errors.New("OIDC is not configured on the cluster")
	// ErrNoMachineConfigSecret is returned when no machine config secret is found for the cluster.
	ErrNoMachineConfigSecret = errors.New("no machine config secret found for cluster")
	// ErrNoControlPlaneBootstrapData is returned when no control plane bootstrap data secret is found for the cluster.
	ErrNoControlPlaneBootstrapData = errors.New("no control plane bootstrap data secret found for cluster")
)
