package auth_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestDynamicAuthenticator_DelegatesToInner verifies that AuthenticateRequest
// delegates to the wrapped authenticator.
func TestDynamicAuthenticator_DelegatesToInner(t *testing.T) {
	t.Parallel()

	inner := &fakeAuthenticator{ok: true}
	dyn := auth.NewDynamicAuthenticator(inner)

	req := newRequestWithNoAuth()

	resp, ok, err := dyn.AuthenticateRequest(req)
	require.NoError(t, err)
	assert.True(t, ok)
	require.NotNil(t, resp)
	assert.Equal(t, "fake-user", resp.User.GetName())
	assert.Equal(t, 1, inner.callCount, "inner authenticator should have been called once")
}

// TestDynamicAuthenticator_Set_SwapsInner verifies that Set replaces the
// inner authenticator and subsequent AuthenticateRequest calls go to the
// new one.
func TestDynamicAuthenticator_Set_SwapsInner(t *testing.T) {
	t.Parallel()

	first := &fakeAuthenticator{ok: true}
	dyn := auth.NewDynamicAuthenticator(first)

	// Swap in a second authenticator.
	second := &fakeAuthenticator{ok: false}
	dyn.Set(second)

	req := newRequestWithNoAuth()

	_, ok, err := dyn.AuthenticateRequest(req)
	require.NoError(t, err)
	assert.False(t, ok, "second authenticator returns false")

	// First should not have been called after Set.
	assert.Equal(t, 0, first.callCount, "first authenticator should not have been called after Set")
	assert.Equal(t, 1, second.callCount, "second authenticator should have been called once")
}

// TestDynamicAuthenticator_ConcurrentAccess verifies that concurrent
// AuthenticateRequest and Set calls don't race or panic.
func TestDynamicAuthenticator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	dyn := auth.NewDynamicAuthenticator(&fakeAuthenticator{ok: true})

	var waitGroup sync.WaitGroup

	// Concurrent readers.
	for range 20 {
		waitGroup.Go(func() {
			req := newRequestWithNoAuth()

			for range 50 {
				_, _, _ = dyn.AuthenticateRequest(req)
			}
		})
	}

	// Concurrent writer.
	waitGroup.Go(func() {
		for range 50 {
			dyn.Set(&fakeAuthenticator{ok: true})
		}
	})

	waitGroup.Wait()

	// If we got here without a race or panic, the test passes.
	// Run with -race to verify thread safety.
}

// TestDynamicAuthenticator_NilInnerPanics verifies that calling
// AuthenticateRequest on a DynamicAuthenticator with a nil inner panics
// (this is a programming error, not a runtime condition).
func TestDynamicAuthenticator_NilInnerPanics(t *testing.T) {
	t.Parallel()

	dyn := auth.NewDynamicAuthenticator(nil)

	req := newRequestWithNoAuth()

	assert.Panics(t, func() {
		_, _, _ = dyn.AuthenticateRequest(req)
	})
}

// TestDynamicAuthenticator_ImplementsInterface verifies that
// *DynamicAuthenticator satisfies authenticator.Request.
func TestDynamicAuthenticator_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ authenticator.Request = (*auth.DynamicAuthenticator)(nil)

	var _ authenticator.Request = auth.NewDynamicAuthenticator(&fakeAuthenticator{})
}

// TestDynamicAuthenticator_WithAnonymousInner verifies that wrapping
// an anonymous authenticator and authenticating returns the anonymous user.
func TestDynamicAuthenticator_WithAnonymousInner(t *testing.T) {
	t.Parallel()

	anon := &fakeAuthenticator{
		ok: true,
		response: &authenticator.Response{
			User: &user.DefaultInfo{Name: "system:anonymous"},
		},
	}

	dyn := auth.NewDynamicAuthenticator(anon)

	req := newRequestWithNoAuth()

	resp, ok, err := dyn.AuthenticateRequest(req)
	require.NoError(t, err)

	assert.True(t, ok)

	assert.Equal(t, "system:anonymous", resp.User.GetName())
}
