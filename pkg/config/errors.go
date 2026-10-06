package config

import "errors"

var (
	// ErrAdminGroupNotSet is returned when the admin group environment variable is not set.
	ErrAdminGroupNotSet = errors.New("admin group is not set, no admin group configured")
	// ErrKommodityDBEnvVarNotSet indicates that the KOMMODITY_DB_URI environment variable is not set.
	ErrKommodityDBEnvVarNotSet = errors.New("KOMMODITY_DB_URI environment variable is not set")
	// ErrInvalidInstanceName is returned when KOMMODITY_INSTANCE_NAME contains
	// characters that would break YAML, HTML selectors, or Kubernetes resource names.
	ErrInvalidInstanceName = errors.New("KOMMODITY_INSTANCE_NAME must be a DNS-1123 label: [a-z0-9-], max 63 chars")
)
