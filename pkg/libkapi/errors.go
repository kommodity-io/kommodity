package libkapi

import (
	"errors"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"
)

var (
	// Storage errors — re-aliased from pkg/libkapi/storage so existing
	// libkapi.Err... references keep working; new code should prefer
	// storage.Err... directly.

	// ErrUnsupportedConnectionScheme is returned when the connection string scheme has no known storage backend.
	ErrUnsupportedConnectionScheme = storage.ErrUnsupportedConnectionScheme
	// ErrEmptyConnectionString is returned when the storage connection string is empty.
	ErrEmptyConnectionString = storage.ErrEmptyConnectionString
	// ErrEmptyStorageEndpoint is returned when an etcd:// or unix:// connection string has no host or path.
	ErrEmptyStorageEndpoint = storage.ErrEmptyStorageEndpoint

	// Auth errors — re-aliased from pkg/libkapi/auth.

	// ErrOIDCIssuerRequired is returned when the OIDC issuer URL is empty.
	ErrOIDCIssuerRequired = auth.ErrOIDCIssuerRequired
	// ErrOIDCClientIDRequired is returned when the OIDC client ID is empty.
	ErrOIDCClientIDRequired = auth.ErrOIDCClientIDRequired
	// ErrAdminGroupRequired is returned when the admin authorizer is used without an admin group.
	ErrAdminGroupRequired = auth.ErrAdminGroupRequired

	// Server errors.

	// ErrGroupVersionNotRegistered is returned when a standard API group has no version registered in the scheme.
	ErrGroupVersionNotRegistered = errors.New("no scheme version registered for group")
	// ErrServerAlreadyStarted is returned when ListenAndServe is called more than once.
	ErrServerAlreadyStarted = errors.New("server already started")
	// ErrServerNotStarted is returned when Shutdown is called before ListenAndServe.
	ErrServerNotStarted = errors.New("server not started")
	// ErrNotImplemented is returned for optional config fields that are reserved but not yet implemented.
	ErrNotImplemented = errors.New("not implemented")
	// ErrServiceResolutionUnsupported is returned by the noopServiceResolver when
	// a remote APIService (Spec.Service != nil) is created, which libkapi does
	// not support.
	ErrServiceResolutionUnsupported = errors.New(
		"service resolution is not supported in libkapi: no Service or Endpoints resources are wired")
)
