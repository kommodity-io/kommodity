// Package v1alpha1 provides error definitions for v1alpha1 objects.
package v1alpha1

import (
	"errors"
)

var (
	// ErrNotOfTypeNamespace indicates that the object is not of type Namespace.
	ErrNotOfTypeNamespace = errors.New("object is not of type Namespace")
	// ErrNameCannotBeChanged indicates that new object of type Namespace has changed name.
	ErrNameCannotBeChanged = errors.New("the Name of type Namespace cannot be changed")
)
