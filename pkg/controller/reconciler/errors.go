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
	// ErrSecretMissingAnnotation is returned when a required annotation is missing from a secret.
	ErrSecretMissingAnnotation = errors.New("secret is missing required annotation")
)
