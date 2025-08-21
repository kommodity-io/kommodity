// Package storage provides error definitions for storage operations.
package storage

import "errors"

var (
	// ErrObjectIsNotANamespace indicates that the object is not a namespace.
	ErrObjectIsNotANamespace = errors.New("object is not a Namespace")
	// ErrObjectIsNotASecret indicates that the object is not a secret.
	ErrObjectIsNotASecret = errors.New("object is not a Secret")
	// ErrObjectIsNotAService indicates that the object is not a service.
	ErrObjectIsNotAService = errors.New("object is not a Service")
	// ErrObjectIsNotAnEndpoint indicates that the object is not an endpoint.
	ErrObjectIsNotAnEndpoint = errors.New("object is not an Endpoint")
)
