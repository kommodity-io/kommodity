package libkapi_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/require"
)

// serverFactoryTestShutdownTimeout bounds the server shutdown run from t.Cleanup.
const serverFactoryTestShutdownTimeout = 10 * time.Second

// loopbackHealthServer implements grpc_health_v1.HealthServer, reporting
// SERVING only if kubeClient (built from Ctx.LoopbackConfig by the
// WithServerFactory registration in TestServerEndToEnd_WithServerFactory) can
// reach the built API server - proving the loopback config handed to a
// ServerFactory is a real, privileged, working client, not just a non-nil
// struct, and that it still works once used from a request handler
// (long after the ServerFactory itself returned) - the pattern the KMS
// service needs.
type loopbackHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer

	kubeClient kubernetes.Interface
}

func (s *loopbackHealthServer) Check(
	ctx context.Context, _ *grpc_health_v1.HealthCheckRequest,
) (*grpc_health_v1.HealthCheckResponse, error) {
	_, err := s.kubeClient.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		//nolint:nilerr // a failed probe is reported as NOT_SERVING, not as an RPC error.
		return &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		}, nil
	}

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// TestServerEndToEnd_WithServerFactory verifies that a single
// WithServerFactory registration can use all three of Ctx's resources
// together: HTTPMux to mount a route, GRPCServer to register a gRPC
// service (proving it multiplexes onto the same listener exactly like
// WithGRPCServerFactory), and LoopbackConfig to build a client the gRPC
// service uses at request time.
func TestServerEndToEnd_WithServerFactory(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-serverfactory.db")

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithServerFactory(func(factoryCtx *libkapi.Ctx) error {
			factoryCtx.HTTPMux().HandleFunc("GET /hello", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hello"))
			})

			kubeClient, err := kubernetes.NewForConfig(factoryCtx.LoopbackConfig())
			if err != nil {
				return fmt.Errorf("failed to build loopback client: %w", err)
			}

			grpc_health_v1.RegisterHealthServer(
				factoryCtx.GRPCServer(), &loopbackHealthServer{kubeClient: kubeClient})

			return nil
		}),
	)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), serverFactoryTestShutdownTimeout)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	libkapi.WaitForHealthz(t, "http://"+addr+"/healthz")

	assertHelloRoute(t, "http://"+addr+"/hello")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_health_v1.NewHealthClient(conn)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()

	resp, err := client.Check(callCtx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.GetStatus())
}

// TestNew_ServerFactoryError verifies that an error returned by a
// ServerFactory fails New instead of silently building a partially
// configured Server.
func TestNew_ServerFactoryError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-serverfactory-error.db")

	_, err := libkapi.New(ctx,
		libkapi.WithAddr(addr),
		libkapi.WithStorage("sqlite://"+dbPath),
		libkapi.WithServerFactory(func(*libkapi.Ctx) error {
			return errGRPCFactoryTest
		}),
	)
	require.ErrorIs(t, err, errGRPCFactoryTest)
}
