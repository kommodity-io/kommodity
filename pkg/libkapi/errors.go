package libkapi

import "errors"

var (
	// ErrUnsupportedConnectionScheme is returned when the connection string scheme has no known storage backend.
	ErrUnsupportedConnectionScheme = errors.New("unsupported connection string scheme")
	// ErrEmptyConnectionString is returned when the storage connection string is empty.
	ErrEmptyConnectionString = errors.New("connection string must not be empty")
	// ErrGroupVersionNotRegistered is returned when a standard API group has no version registered in the scheme.
	ErrGroupVersionNotRegistered = errors.New("no scheme version registered for group")
	// ErrServerAlreadyStarted is returned when ListenAndServe is called more than once.
	ErrServerAlreadyStarted = errors.New("server already started")
	// ErrServerNotStarted is returned when Shutdown is called before ListenAndServe.
	ErrServerNotStarted = errors.New("server not started")
	// ErrNotImplemented is returned for optional config fields that are reserved but not yet implemented.
	ErrNotImplemented = errors.New("not implemented")
	// ErrEmptyStorageEndpoint is returned when an etcd:// or unix:// connection string has no host or path.
	ErrEmptyStorageEndpoint = errors.New("connection string must specify a host or path")
)
