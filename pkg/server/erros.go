// Package server provides error definitions for server operations.
package server

import "errors"

var (
	// ErrMissingOIDCConfig indicates that the OIDC configuration is not set.
	ErrMissingOIDCConfig = errors.New("OIDC configuration is not set")
)
