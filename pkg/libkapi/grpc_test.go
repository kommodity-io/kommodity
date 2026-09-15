package libkapi_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/require"
)

// grpcTestShutdownTimeout bounds the server shutdown run from t.Cleanup.
const grpcTestShutdownTimeout = 10 * time.Second

// errGRPCFactoryTest is returned by the failing factory in
// TestNew_GRPCServerFactoryError.
var errGRPCFactoryTest = errors.New("boom")

// TestServerEndToEnd_WithGRPCServerFactory verifies that a gRPC service
// registered via WithGRPCServerFactory is reachable over the same address
// and port as the built API server, and that ordinary HTTP traffic (here,
// /healthz) keeps working on that same listener - proving muxHandler's
// Content-Type-based routing and the h2c upgrade both work end-to-end.
func TestServerEndToEnd_WithGRPCServerFactory(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-grpc.db")

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithGRPCServerFactory(func(grpcServer *grpc.Server) error {
			grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

			return nil
		}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), grpcTestShutdownTimeout)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_health_v1.NewHealthClient(conn)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()

	resp, err := client.Check(callCtx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.GetStatus())

	// The HTTP path must still work on the very same listener after a real
	// gRPC call has gone through it.
	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")
}

// TestNew_GRPCServerFactoryError verifies that an error returned by a
// GRPCServerFactory fails New instead of silently building a Server with a
// partially configured gRPC server.
func TestNew_GRPCServerFactoryError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-grpc-error.db")

	_, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithGRPCServerFactory(func(*grpc.Server) error {
			return errGRPCFactoryTest
		}),
	)
	require.ErrorIs(t, err, errGRPCFactoryTest)
}
