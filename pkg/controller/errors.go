// Package controller provides error definitions for controller operations.
//
//nolint:lll
package controller

import "errors"

var (
	// ErrWebhookServerCertsNotConfigured is returned when the webhook server certificate is not configured.
	ErrWebhookServerCertsNotConfigured = errors.New("webhook server requires a certificates to be configured")
	// ErrWebhookServerCertKeyNotConfigured is returned when the webhook server certificate and key is not configured.
	ErrWebhookServerCertKeyNotConfigured = errors.New("webhook server requires both certificate and key to be configured")
	// ErrWebhookServerCertKeyNotInSameDir is returned when the webhook server certificate and key are not in the same directory.
	ErrWebhookServerCertKeyNotInSameDir = errors.New("webhook server certificate and key must be in the same directory")
)
