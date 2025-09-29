// Package config provides error definitions for configuration operations.
package config

import "errors"

var (
	// ErrAdminGroupNotSet is returned when the admin group environment variable is not set.
	ErrAdminGroupNotSet = errors.New("admin group is not set, no admin group configured")
)
