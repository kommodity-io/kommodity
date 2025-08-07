package genericserver

import (
	"errors"
)

var (
	// ErrMethodNotAllowed indicates that the method is not allowed.
	ErrMethodNotAllowed = errors.New("method not allowed")
	// ErrEncodingFailed indicates that encoding a response failed.
	ErrEncodingFailed = errors.New("encoding failed")
	// ErrResourceNotFound indicates that a resource was not found.
	ErrResourceNotFound = errors.New("resource not found")
	// ErrFailedToCreateResource indicates that creating a resource failed.
	ErrFailedToCreateResource = errors.New("failed to create resource")
	// ErrNotValidatable indicates that the object does not implement the Validatable interface.
	ErrNotValidatable = errors.New("object does not implement Validatable interface")
	// ErrNotUpdatedObjectInfo indicates that the object does not implement the UpdatedObjectInfo interface.
	ErrNotUpdatedObjectInfo = errors.New("object does not implement UpdatedObjectInfo interface")
)
