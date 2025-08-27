// Package server provides error definitions for server operations.
package server

import "errors"

var (
	// ErrInvalidKommodityDebugVar indicates that the KOMMODITY_DEBUG environment variable not a valid bool format.
	ErrInvalidKommodityDebugVar = errors.New("KOMMODITY_DEBUG environment variable not a valid bool format")
)
