// Package api provides error definitions for metadata API operations.
package api

import "errors"

var (
	// ErrFailedToFindContext is returned when the context for a cluster is not found.
	ErrFailedToFindContext = errors.New("failed to find context for cluster")
	// ErrOIDCNotConfigured is returned when OIDC is not configured on the cluster.
	ErrOIDCNotConfigured = errors.New("OIDC is not configured on the cluster")
)
