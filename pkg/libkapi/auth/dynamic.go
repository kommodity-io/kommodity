package auth

import (
	"net/http"
	"sync"

	"k8s.io/apiserver/pkg/authentication/authenticator"
)

// DynamicAuthenticator is a thread-safe authenticator.Request wrapper whose
// inner authenticator can be swapped at runtime via Set. It allows the SA
// authenticator to be installed after the server starts, when the signing
// key can be loaded from a persisted Secret.
type DynamicAuthenticator struct {
	mu    sync.RWMutex
	inner authenticator.Request
}

// NewDynamicAuthenticator wraps inner in a DynamicAuthenticator.
func NewDynamicAuthenticator(inner authenticator.Request) *DynamicAuthenticator {
	return &DynamicAuthenticator{inner: inner}
}

// AuthenticateRequest delegates to the current inner authenticator.
//
//nolint:wrapcheck // passthrough to the wrapped authenticator.
func (d *DynamicAuthenticator) AuthenticateRequest(req *http.Request) (*authenticator.Response, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.AuthenticateRequest(req)
}

// Set replaces the inner authenticator. Safe to call concurrently with
// AuthenticateRequest.
func (d *DynamicAuthenticator) Set(inner authenticator.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner = inner
}
