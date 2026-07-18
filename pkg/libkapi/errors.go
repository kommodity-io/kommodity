package libkapi

import (
	"errors"

	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"
)

var (
	// ErrUnsupportedConnectionScheme is returned when the connection string scheme has no known storage backend.
	//
	// Re-aliased from pkg/libkapi/storage so existing libkapi.Err... references
	// keep working; new code should prefer storage.ErrUnsupportedConnectionScheme.
	ErrUnsupportedConnectionScheme = storage.ErrUnsupportedConnectionScheme
	// ErrEmptyConnectionString is returned when the storage connection string is empty.
	//
	// Re-aliased from pkg/libkapi/storage so existing libkapi.Err... references
	// keep working; new code should prefer storage.ErrEmptyConnectionString.
	ErrEmptyConnectionString = storage.ErrEmptyConnectionString
	// ErrEmptyStorageEndpoint is returned when an etcd:// or unix:// connection string has no host or path.
	//
	// Re-aliased from pkg/libkapi/storage so existing libkapi.Err... references
	// keep working; new code should prefer storage.ErrEmptyStorageEndpoint.
	ErrEmptyStorageEndpoint = storage.ErrEmptyStorageEndpoint
	// ErrGroupVersionNotRegistered is returned when a standard API group has no version registered in the scheme.
	ErrGroupVersionNotRegistered = errors.New("no scheme version registered for group")
	// ErrServerAlreadyStarted is returned when ListenAndServe is called more than once.
	ErrServerAlreadyStarted = errors.New("server already started")
	// ErrServerNotStarted is returned when Shutdown is called before ListenAndServe.
	ErrServerNotStarted = errors.New("server not started")
	// ErrNotImplemented is returned for optional config fields that are reserved but not yet implemented.
	ErrNotImplemented = errors.New("not implemented")
)
