// Package rest provides error definitions for attestation rest operations.
package rest

import "errors"

var (
	// ErrInvalidNonce is returned when the nonce is invalid.
	ErrInvalidNonce = errors.New("invalid nonce")
	// ErrExpiredNonce is returned when the nonce is expired.
	ErrExpiredNonce = errors.New("expired nonce")
	// ErrIPMismatch is returned when the IP address does not match the nonce's bound IP.
	ErrIPMismatch = errors.New("ip address does not match nonce's bound IP")
	// ErrNonceNotFound is returned when the nonce is not found.
	ErrNonceNotFound = errors.New("failed to find nonce label in attestation report config map")
	// ErrNoPEMBlock is returned when no PEM block is found in the provided data.
	ErrNoPEMBlock = errors.New("no PEM block found")
	// ErrNoPCRSelection is returned when no PCR selection is found in the attestation report.
	ErrNoPCRSelection = errors.New("no PCR selection found in attestation report")
	// ErrNonceMismatch is returned when the nonce does not match the attestation report.
	ErrNonceMismatch = errors.New("nonce mismatch in attestation report")
	// ErrPCRDigestMismatch is returned when the PCR digest does not match the attestation report.
	ErrPCRDigestMismatch = errors.New("PCR digest mismatch in attestation report")
	// ErrTPMSignatureInvalid is returned when the TPM signature is invalid.
	ErrTPMSignatureInvalid = errors.New("invalid TPM signature")
	// ErrMissingPCR is returned when a required PCR is missing from the attestation report.
	ErrMissingPCR = errors.New("missing required PCR in attestation report")
	// ErrUnexpectedSignatureAlgorithm is returned when the signature algorithm is unexpected.
	ErrUnexpectedSignatureAlgorithm = errors.New("unexpected signature algorithm in attestation report")
	// ErrUnexpectedAttestationType is returned when the attestation type is unexpected.
	ErrUnexpectedAttestationType = errors.New("unexpected attestation type")
	// ErrMissingComponent is returned when a required component is missing from the attestation report.
	ErrMissingComponent = errors.New("missing required component in attestation report")
	// ErrComponentMismatch is returned when a component does not match the attestation report.
	ErrComponentMismatch = errors.New("component mismatch in attestation report")
	// ErrPCRMismatch is returned when a PCR does not match the attestation report.
	ErrPCRMismatch = errors.New("PCR mismatch in attestation report")
)
