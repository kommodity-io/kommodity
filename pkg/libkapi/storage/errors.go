package storage

import "errors"

var (
	// ErrUnsupportedConnectionScheme is returned when the connection string scheme has no known storage backend.
	ErrUnsupportedConnectionScheme = errors.New("unsupported connection string scheme")
	// ErrEmptyConnectionString is returned when the storage connection string is empty.
	ErrEmptyConnectionString = errors.New("connection string must not be empty")
	// ErrEmptyStorageEndpoint is returned when an etcd:// or unix:// connection string has no host or path.
	ErrEmptyStorageEndpoint = errors.New("connection string must specify a host or path")
	// ErrKineNotReady is returned when the embedded Kine endpoint does not
	// become ready to accept connections within the readiness timeout.
	ErrKineNotReady = errors.New("kine did not become ready")
)
