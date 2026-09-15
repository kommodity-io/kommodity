package libkapi

import (
	"errors"

	"github.com/kommodity-io/kommodity/pkg/libkapi/apiserver"
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
	// ErrKineNotReady is returned when the embedded Kine endpoint does not
	// become ready to accept connections within the readiness timeout.
	ErrKineNotReady = storage.ErrKineNotReady

	// Auth errors — re-aliased from pkg/libkapi/auth.

	// ErrOIDCIssuerRequired is returned when the OIDC issuer URL is empty.
	ErrOIDCIssuerRequired = auth.ErrOIDCIssuerRequired
	// ErrOIDCClientIDRequired is returned when the OIDC client ID is empty.
	ErrOIDCClientIDRequired = auth.ErrOIDCClientIDRequired
	// ErrAdminGroupRequired is returned when the admin authorizer is used without an admin group.
	ErrAdminGroupRequired = auth.ErrAdminGroupRequired

	// API server errors — re-aliased from pkg/libkapi/apiserver.

	// ErrGroupVersionNotRegistered is returned when a standard API group has no version registered in the scheme.
	ErrGroupVersionNotRegistered = apiserver.ErrGroupVersionNotRegistered
	// ErrServiceResolutionUnsupported is returned by the noopServiceResolver when
	// a remote APIService (Spec.Service != nil) is created, which libkapi does
	// not support.
	ErrServiceResolutionUnsupported = apiserver.ErrServiceResolutionUnsupported

	// Server errors.

	// ErrServerAlreadyStarted is returned when ListenAndServe is called more than once.
	ErrServerAlreadyStarted = errors.New("server already started")
	// ErrServerNotStarted is returned when Shutdown is called before ListenAndServe.
	ErrServerNotStarted = errors.New("server not started")
	// ErrNotImplemented is returned for optional config fields that are reserved but not yet implemented.
	ErrNotImplemented = errors.New("not implemented")
	// ErrLeaderElectionIDRequired is returned when WithLeaderElection is called with an empty ID.
	ErrLeaderElectionIDRequired = errors.New("leader election ID is required when using WithLeaderElection")

	// Garbage collector errors — see WithGarbageCollector.

	// ErrGarbageCollectorClientBuild is returned when constructing the typed,
	// metadata, or discovery client for the garbage collector fails.
	ErrGarbageCollectorClientBuild = errors.New("failed to build garbage collector client")
	// ErrGarbageCollectorInit is returned when the garbage collector fails to initialize.
	ErrGarbageCollectorInit = errors.New("failed to initialize garbage collector")
	// ErrGarbageCollectorWebhookNotReady is returned when WithWebhookServer
	// was used but its TLS listener never became dialable within the
	// garbage collector's startup readiness timeout.
	ErrGarbageCollectorWebhookNotReady = errors.New("garbage collector webhook server was not ready in time")

	// Webhook certificate errors — see webhookcertsecret.go.

	// ErrWebhookCertDataMissing is returned when a webhook cert Secret
	// exists but its tls.crt/tls.key data is missing.
	ErrWebhookCertDataMissing = errors.New("webhook cert secret exists but tls.crt/tls.key data is missing")
	// ErrWebhookCertPEMDecodeFailed is returned when a webhook certificate's
	// PEM block can't be decoded.
	ErrWebhookCertPEMDecodeFailed = errors.New("failed to decode webhook certificate PEM block")
	// ErrWebhookCertDNSNamesRequired is returned when generating a webhook
	// certificate with no DNS names to embed as its Common Name/SANs.
	ErrWebhookCertDNSNamesRequired = errors.New("webhook certificate generation requires at least one DNS name")
)
