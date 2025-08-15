// Package storage provides error definitions for storage operations.
package storage

import "errors"

var (
	// ErrObjectIsNotANamespace indicates that the object is not a namespace.
	ErrObjectIsNotANamespace = errors.New("object is not a Namespace")
	// ErrObjectIsNotASecret indicates that the object is not a secret.
	ErrObjectIsNotASecret = errors.New("object is not a Secret")
)
