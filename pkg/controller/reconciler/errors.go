package reconciler

import "errors"

var (
	// ErrUnsupportedProvider is returned when an infrastructure provider is not supported.
	ErrUnsupportedProvider = errors.New("infrastructure provider is not supported")
	// ErrValueNotFoundInSecret is returned when a value is not found in a secret.
	ErrValueNotFoundInSecret = errors.New("value not found in secret")
	// ErrValueNotFoundInConfigMap is returned when a value is not found in a configmap.
	ErrValueNotFoundInConfigMap = errors.New("value not found in configmap")
	// ErrSecretMissingAnnotation is returned when a required annotation is missing from a secret.
	ErrSecretMissingAnnotation = errors.New("secret is missing required annotation")
	// ErrClusterNotReady indicates the downstream cluster is not reachable.
	ErrClusterNotReady = errors.New("downstream cluster not ready")
	// ErrNoHealthyEndpoints is returned when no healthy Kubernetes API endpoints are found.
	ErrNoHealthyEndpoints = errors.New("no healthy kubernetes API endpoints")
	// ErrControlPlaneLeaseExpired is returned when control plane component leases are expired.
	ErrControlPlaneLeaseExpired = errors.New("control plane component leases expired")
)
