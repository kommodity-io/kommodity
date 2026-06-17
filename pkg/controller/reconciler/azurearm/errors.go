package azurearm

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrCredentialSecretNotFound indicates the credential Secret referenced by a
	// resource (or the configured default) could not be found.
	ErrCredentialSecretNotFound = errors.New("azure credential secret not found")
	// ErrCredentialSecretIncomplete indicates the credential Secret is missing one
	// or more required keys.
	ErrCredentialSecretIncomplete = errors.New("azure credential secret is missing required keys")
	// ErrUnsupportedResourceType indicates the object does not implement the
	// expected ASO ARM conversion interfaces.
	ErrUnsupportedResourceType = errors.New("unsupported azure resource type")
	// ErrARMIDUnresolvable indicates the fully-qualified ARM ID for a resource
	// could not be constructed (e.g. an owner is not yet provisioned).
	ErrARMIDUnresolvable = errors.New("unable to resolve fully-qualified ARM ID")
	// ErrReferenceUnresolved indicates a resource reference could not be resolved
	// to an ARM ID.
	ErrReferenceUnresolved = errors.New("unable to resolve resource reference to an ARM ID")
	// ErrARMTerminal indicates a terminal ARM error (HTTP 4xx) that should not be
	// retried without a spec change.
	ErrARMTerminal = errors.New("terminal ARM error")
)

// ARMRateLimitedError is returned when ARM responds with HTTP 429. It carries the
// Retry-After duration so the reconciler can schedule the next attempt precisely.
type ARMRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *ARMRateLimitedError) Error() string {
	return fmt.Sprintf("ARM rate limited; retry after %s", e.RetryAfter)
}

// IsARMRateLimited returns the ARMRateLimitedError if err wraps one.
func IsARMRateLimited(err error) (*ARMRateLimitedError, bool) {
	rateLimitErr, ok := errors.AsType[*ARMRateLimitedError](err)

	return rateLimitErr, ok
}
