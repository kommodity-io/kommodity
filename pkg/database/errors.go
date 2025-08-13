// Package database provides error definitions for database operations.
package database

import "errors"

var (
	// ErrKommodityDBEnvVarNotSet indicates that the KOMMODITY_DB_URI environment variable is not set.
	ErrKommodityDBEnvVarNotSet = errors.New("KOMMODITY_DB_URI environment variable is not set")
	ErrCantLoadDotEnv = errors.New(".env file not found or can't be loaded")
)
