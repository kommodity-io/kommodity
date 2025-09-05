// Package storage provides error definitions for storage operations.
package storage

import "errors"

var (
	// ErrObjectIsNotANamespace indicates that the object is not a namespace.
	ErrObjectIsNotANamespace = errors.New("object is not a Namespace")
	// ErrObjectIsNotASecret indicates that the object is not a secret.
	ErrObjectIsNotASecret = errors.New("object is not a Secret")
	// ErrObjectIsNotAConfigMap indicates that the object is not a ConfigMap.
	ErrObjectIsNotAConfigMap = errors.New("object is not a ConfigMap")
	// ErrObjectIsNotAService indicates that the object is not a service.
	ErrObjectIsNotAService = errors.New("object is not a Service")
	// ErrObjectIsNotAnEndpoint indicates that the object is not an endpoint.
	ErrObjectIsNotAnEndpoint = errors.New("object is not an Endpoint")
	// ErrObjectIsNotAnEvent indicates that the object is not an event.
	ErrObjectIsNotAnEvent = errors.New("object is not an Event")
	// ErrFieldIsNull indicates that the field is null.
	ErrFieldIsNull = errors.New("field must not be null")
)
