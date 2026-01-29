// Package reconciler provides error definitions for reconciler operations.
package reconciler

import "errors"

var (
	// ErrUnsupportedProvider is returned when an infrastructure provider is not supported.
	ErrUnsupportedProvider = errors.New("infrastructure provider is not supported")
	// ErrValueNotFoundInSecret is returned when a value is not found in a secret.
	ErrValueNotFoundInSecret = errors.New("value not found in secret")
	// ErrValueNotFoundInConfigMap is returned when a value is not found in a configmap.
	ErrValueNotFoundInConfigMap = errors.New("value not found in configmap")
	// ErrClusterNotReady indicates the downstream cluster is not reachable.
	ErrClusterNotReady = errors.New("downstream cluster not ready")
)
