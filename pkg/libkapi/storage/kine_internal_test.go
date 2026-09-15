package storage

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errSimulatedConnectionFailure stands in for whatever error k3s-io/kine's
// generic.Open would have panicked with (see ErrKineStartPanicked).
var errSimulatedConnectionFailure = errors.New("simulated connection failure")

// TestStartKineRecoversFromListenPanic simulates the upstream k3s-io/kine
// failure mode described by ErrKineStartPanicked - generic.Open proceeding
// with a nil *sql.DB after exhausting its internal retry loop - by swapping
// out endpointListen for a stub that panics, without waiting on a real
// 5-minute database outage. startKine must recover and return a wrapped
// error rather than letting the panic escape.
//
//nolint:paralleltest // mutates the package-level endpointListen seam that
// storage_test.go's parallel tests call indirectly through storage.Resolve.
func TestStartKineRecoversFromListenPanic(t *testing.T) {
	original := endpointListen

	t.Cleanup(func() { endpointListen = original })

	endpointListen = func(context.Context, endpoint.Config) (endpoint.ETCDConfig, error) {
		panic(errSimulatedConnectionFailure)
	}

	var kineWaitGroup sync.WaitGroup

	endpoints, cleanup, err := startKine(context.Background(), "postgres://example", &kineWaitGroup)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrKineStartPanicked)
	require.ErrorIs(t, err, errSimulatedConnectionFailure)
	assert.Nil(t, endpoints)
	assert.Nil(t, cleanup)
}
