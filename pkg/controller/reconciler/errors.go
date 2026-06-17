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
	// ErrSecretMissingLabel is returned when a required label is missing from a secret.
	ErrSecretMissingLabel = errors.New("secret is missing required label")
	// ErrClusterNotReady indicates the downstream cluster is not reachable.
	ErrClusterNotReady = errors.New("downstream cluster not ready")
	// ErrTokenNotPopulated indicates the service account token secret has not yet been
	// populated by the TokensController.
	ErrTokenNotPopulated = errors.New("service account token not yet populated in secret")
	// ErrClusterMissingAnnotation is returned when a required annotation is missing from a Cluster.
	ErrClusterMissingAnnotation = errors.New("cluster is missing required annotation")
	// ErrSecretOwnedByAnotherCluster is returned when a materialized Secret already belongs to a
	// different Cluster. Taking it over would steal that cluster's credentials and, on this
	// cluster's teardown, garbage collect a Secret the other cluster still depends on. This is the
	// signature of a values file copied from another cluster without updating provider.secret.name.
	ErrSecretOwnedByAnotherCluster = errors.New("secret is materialized for another cluster")
)
