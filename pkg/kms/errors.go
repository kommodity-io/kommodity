package kms

import "errors"

var (
	// ErrEmptyClientContext is an error that indicates the client context is empty.
	ErrEmptyClientContext = errors.New("client context is empty")
	// ErrEmptyData is an error that indicates the data is empty.
	ErrEmptyData = errors.New("data is empty")
	// ErrCipherTooShort is an error that indicates the ciphertext is too short.
	ErrCipherTooShort = errors.New("ciphertext too short")
	// ErrIPMismatch is an error that indicates the caller IP doesn't match the sealed IP.
	ErrIPMismatch = errors.New("caller IP does not match sealed IP")
	// ErrNoMatchingSecret is an error that indicates no volume group could decrypt the provided ciphertext.
	ErrNoMatchingSecret = errors.New("no volume key set could decrypt the provided ciphertext")
	// ErrNoVolumeKeySets is an error that indicates no volume key sets were found in the secret.
	ErrNoVolumeKeySets = errors.New("no volume key sets found in secret")
	// ErrNoValidClientIP is an error that indicates no valid client IP could be resolved.
	ErrNoValidClientIP = errors.New("no valid client IP could be resolved")
	// ErrClusterNotResolved is an error that indicates the owning cluster of the requesting
	// node could not be determined from the request's client IP.
	ErrClusterNotResolved = errors.New("could not resolve owning cluster for node")
	// ErrAmbiguousSecret is an error that indicates more than one KMS secret matched the
	// node UUID label, which should never happen in a healthy system.
	ErrAmbiguousSecret = errors.New("more than one KMS secret matches node UUID")
	// ErrInvalidClusterName is an error that indicates the resolved cluster name is not a
	// valid DNS-1123 label and therefore cannot be used as a secret name prefix.
	ErrInvalidClusterName = errors.New("cluster name is not a valid DNS-1123 label")
	// ErrSecretNotFound is an error that indicates no KMS secret was found for the node UUID.
	ErrSecretNotFound = errors.New("no KMS secret found for node UUID")
)
