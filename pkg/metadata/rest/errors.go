// Package rest provides error definitions for metadata rest operations.
package rest

import "errors"

var (
	// ErrUnexpectedResponse is returned when the response from the endpoint is unexpected.
	ErrUnexpectedResponse = errors.New("unexpected response from endpoint")
)
