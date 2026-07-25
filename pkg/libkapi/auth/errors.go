package auth

import "errors"

var (
	// ErrOIDCIssuerRequired is returned when the OIDC issuer URL is empty.
	ErrOIDCIssuerRequired = errors.New("OIDC issuer URL is required")
	// ErrOIDCClientIDRequired is returned when the OIDC client ID is empty.
	ErrOIDCClientIDRequired = errors.New("OIDC client ID is required")
	// ErrAdminGroupRequired is returned when the admin authorizer is used without an admin group.
	ErrAdminGroupRequired = errors.New("admin group is required when using WithAdminAuthorizer")
	// ErrAuthorizerNil is returned when WithAuthorizer is called with a nil authorizer.
	ErrAuthorizerNil = errors.New("authorizer must not be nil")
	// ErrPEMDecodeFailed is returned when a PEM block cannot be decoded.
	ErrPEMDecodeFailed = errors.New("failed to decode PEM block")
	// ErrKeyParseFailed is returned when a private key cannot be parsed.
	ErrKeyParseFailed = errors.New("failed to parse private key")
	// ErrKeyNotRSA is returned when a private key is not RSA.
	ErrKeyNotRSA = errors.New("private key is not RSA")
	// ErrSigningKeyDataMissing is returned when the signing key Secret exists but the key data is missing.
	ErrSigningKeyDataMissing = errors.New("signing key secret exists but key data is missing")
	// ErrResourceNotSupported is returned for resources that libkapi doesn't store.
	ErrResourceNotSupported = errors.New("not supported in libkapi")
)
