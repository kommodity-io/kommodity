// Package server provides error definitions for server operations.
package server

import "errors"

var (
	// ErrInvalidKommodityDebugVar indicates that the KOMMODITY_DEBUG environment variable not a valid bool format.
	ErrInvalidKommodityDebugVar = errors.New("KOMMODITY_DEBUG environment variable not a valid bool format")
	// ErrFailedToAppendCAData indicates that appending the CA certificate failed.
	ErrFailedToAppendCAData = errors.New("failed to append CA cert")
	// ErrMissingOIDCConfig indicates that the OIDC configuration is not set.
	ErrMissingOIDCConfig = errors.New("OIDC configuration is not set")
	// ErrTimeoutWaitingForCRD indicates that the timeout waiting for CRD to be established.
	ErrTimeoutWaitingForCRD = errors.New("timeout waiting for CRD to be established")
	// ErrNoAdminGroupConfigured indicates that no admin group is configured.
	ErrNoAdminGroupConfigured = errors.New("no admin group configured")
)
