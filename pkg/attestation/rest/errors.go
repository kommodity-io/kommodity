// Package rest provides error definitions for attestation rest operations.
package rest

import "errors"

var (
	// ErrInvalidNonce is returned when the nonce is invalid.
	ErrInvalidNonce = errors.New("invalid nonce")
	// ErrExpiredNonce is returned when the nonce is expired.
	ErrExpiredNonce = errors.New("expired nonce")
	// ErrNoMachineFound is returned when no machine is found for the given criteria.
	ErrNoMachineFound = errors.New("no machine found")
	// ErrIPMismatch is returned when the IP address does not match the nonce's bound IP.
	ErrIPMismatch = errors.New("ip address does not match nonce's bound IP")
)
