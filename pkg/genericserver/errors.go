package genericserver

import (
	"errors"
)

var (
	ErrMethodNotAllowed       = errors.New("method not allowed")
	ErrResourceNotFound       = errors.New("resource not found")
	ErrFailedToCreateResource = errors.New("failed to create resource")
)
