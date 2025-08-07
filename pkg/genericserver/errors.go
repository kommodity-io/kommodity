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
)
