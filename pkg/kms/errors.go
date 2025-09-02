// Package kms provides error definitions for kms operations.
package kms

import "errors"

var (
	// ErrEmptyClientContext is an error that indicates the client context is empty.
	ErrEmptyClientContext = errors.New("client context is empty")
	// ErrEmptyData is an error that indicates the data is empty.
	ErrEmptyData = errors.New("data is empty")
)
