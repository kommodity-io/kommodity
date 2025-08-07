// Package storage provides common storage errors.
package storage

import "errors"

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("resource not found")
	// ErrNamespaceNotFound is returned when the namespace is not found in the request context.
	ErrNamespaceNotFound = errors.New("namespace not found in request context")
	// ErrResourceExists is returned when a resource already exists.
	ErrResourceExists = errors.New("resource already exists")
	// ErrRuntimeObjectConversion is returned when a value cannot be converted to a runtime.Object.
	ErrRuntimeObjectConversion = errors.New("value cannot be converted to runtime.Object")
)
