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
)
