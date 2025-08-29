// Package provider provides error definitions for provider operations.
package provider

import "errors"

var (
	// ErrMissingCRDName indicates that a CRD object is missing its metadata.name field.
	ErrMissingCRDName = errors.New("CRD object is missing metadata.name")
)
