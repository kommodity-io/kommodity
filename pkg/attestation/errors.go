// Package attestation provides error definitions for attestation operations.
package attestation

import "errors"

var (
	// ErrInvalidNounce is returned when the nonce is invalid.
	ErrInvalidNounce = errors.New("invalid nonce")
	// ErrExpiredNounce is returned when the nonce is expired.
	ErrExpiredNounce = errors.New("expired nonce")
	// ErrNoMachineFound is returned when no machine is found for the given criteria.
	ErrNoMachineFound = errors.New("no machine found")
)
