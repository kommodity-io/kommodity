// Package config provides error definitions for configuration operations.
package config

import "errors"

var (
	// ErrAdminGroupNotSet is returned when the admin group environment variable is not set.
	ErrAdminGroupNotSet = errors.New("admin group is not set, no admin group configured")
	// ErrKommodityDBEnvVarNotSet indicates that the KOMMODITY_DB_URI environment variable is not set.
	ErrKommodityDBEnvVarNotSet = errors.New("KOMMODITY_DB_URI environment variable is not set")
	// ErrKommodityKineEnvVarNotSet indicates that the KOMMODITY_KINE_URI environment variable is not set.
	ErrKommodityKineEnvVarNotSet = errors.New("KOMMODITY_KINE_URI environment variable is not set")
)
