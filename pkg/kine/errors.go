// Package kine provides error definitions for kine operations.
package kine

import "errors"

var (
	// ErrKommodityKineEnvVarNotSet indicates that the KOMMODITY_KINE_URI environment variable is not set.
	ErrKommodityKineEnvVarNotSet = errors.New("KOMMODITY_KINE_URI environment variable is not set")
)
