// Package server provides error definitions for server operations.
package server

import "errors"

var (
	// ErrInvalidKommodityDebugVar indicates that the KOMMODITY_DEBUG environment variable not a valid bool format.
	ErrInvalidKommodityDebugVar = errors.New("KOMMODITY_DEBUG environment variable not a valid bool format")
	// ErrFailedToAppendCAData indicates that appending the CA certificate failed.
	ErrFailedToAppendCAData = errors.New("failed to append CA cert")
	// ErrMissingOIDCConfig indicates that the OIDC configuration is not set.
	ErrMissingOIDCConfig = errors.New("OIDC configuration is not set")
	// ErrTimeoutWaitingForCRD indicates that the timeout waiting for CRD to be established.
	ErrTimeoutWaitingForCRD = errors.New("timeout waiting for CRD to be established")
	// ErrTimeoutWaitingForWebhook indicates that the timeout waiting for webhook to be ready.
	ErrTimeoutWaitingForWebhook = errors.New("timeout waiting for webhook to be ready")
	// ErrNoAdminGroupConfigured indicates that no admin group is configured.
	ErrNoAdminGroupConfigured = errors.New("no admin group configured")
	// ErrWebhookServerCertsNotConfigured is returned when the webhook server certificate is not configured.
	ErrWebhookServerCertsNotConfigured = errors.New("webhook server requires a certificates to be configured")
	// ErrWebhookServerCertKeyNotConfigured is returned when the webhook server certificate and key is not configured.
	ErrWebhookServerCertKeyNotConfigured = errors.New("webhook server requires both certificate and key to be configured")
	// ErrFailedToDecodePEMBlock indicates that decoding a PEM block failed.
	ErrFailedToDecodePEMBlock = errors.New("failed to decode PEM block")
	// ErrPrivateKeyNotRSA indicates that the private key is not RSA.
	ErrPrivateKeyNotRSA = errors.New("private key is not RSA")
	// ErrFailedToParsePrivateKey indicates that failed to parse private key as PKCS1 or PKCS8.
	ErrFailedToParsePrivateKey = errors.New("failed to parse private key as PKCS1 or PKCS8")
)
