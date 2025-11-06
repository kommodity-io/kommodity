// Package provider provides error definitions for provider operations.
package provider

import "errors"

var (
	// ErrSpecGroupMissing indicates that a CRD object is missing its spec.group field.
	ErrSpecGroupMissing = errors.New("CRD object is missing spec.group")
	// ErrFailedToConvertWebhook indicates a failure to convert a webhook from unstructured to map[string]any.
	ErrFailedToConvertWebhook = errors.New("failed to convert webhook from unstructured to map[string]any")
)
