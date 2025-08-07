package genericserver

import "fmt"

var (
	ErrMethodNotAllowed       = fmt.Errorf("method not allowed")
	ErrResourceNotFound       = fmt.Errorf("resource not found")
	ErrFailedToCreateResource = fmt.Errorf("failed to create resource")
)
