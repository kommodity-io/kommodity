// Package attestation provides error definitions for attestation operations.
package attestation

import "errors"

var (
	// ErrInvalidNonce is returned when the nonce is invalid.
	ErrInvalidNonce = errors.New("invalid nonce")
	// ErrExpiredNonce is returned when the nonce is expired.
	ErrExpiredNonce = errors.New("expired nonce")
	// ErrNoMachineFound is returned when no machine is found for the given criteria.
	ErrNoMachineFound = errors.New("no machine found")
)
