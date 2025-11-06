// Package reconciler provides error definitions for reconciler operations.
package reconciler

import "errors"

var (
	// ErrUnsupportedProvider is returned when an infrastructure provider is not supported.
	ErrUnsupportedProvider = errors.New("infrastructure provider is not supported")
)
