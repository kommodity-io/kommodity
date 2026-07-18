package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"
)

// TestResolveKineLifecycle validates the second highest-risk design
// decision: driving Kine through k3s-io/kine/pkg/endpoint.Listen directly
// (see kine.go) starts a real, reachable etcd3 endpoint, and that
// canceling its context cleanly stops it - proving no subprocess is needed
// and shutdown is not "fire and forget" the way pkg/kine/server.go's
// CLI-wrapper-based StartKine is today.
func TestResolveKineLifecycle(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "libkapi.db")

	ctx, cancel := context.WithCancel(context.Background())

	handle, err := storage.Resolve(ctx, "sqlite://"+dbPath)
	require.NoError(t, err)
	require.NotEmpty(t, handle.Endpoints(), "expected at least one resolved endpoint")

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   handle.Endpoints(),
		DialTimeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	})
	require.NoError(t, err)

	// Kine only implements the specific If/Then/Else Txn shape that
	// k8s.io/apiserver's etcd3 storage layer generates for writes (it
	// rejects both a bare Put RPC and an arbitrary Txn). Exercising that
	// exact write path belongs to the storage-layer integration test once
	// registry.go exists; here it is enough to prove the endpoint is a live,
	// reachable etcd3 server.
	_, err = client.Get(ctx, "/libkapi/spike-test")
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)

	done := make(chan struct{})

	go func() {
		handle.Close()
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		assert.Fail(t, "handle.Close() did not return within 5s of context cancellation")
	}
}
