package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/k3s-io/kine/pkg/endpoint"
	apiserverstorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/feature"
)

const kineSocketFileName = "kine.sock"

// Kine tuning defaults, mirrored from github.com/k3s-io/kine/pkg/app's CLI
// flag defaults (pkg/app/app.go). endpoint.Listen has none of its own: it is
// a lower-level entry point than the CLI wrapper, so callers driving it
// directly (as startKine does, see the comment below) must supply these
// themselves or Kine's backend rejects the zero values outright (e.g.
// "compact-batch-size 0 too small: must be at least 100").
const (
	kineNotifyInterval        = 5 * time.Second
	kineEmulatedETCDVersion   = "3.5.13"
	kineCompactInterval       = 5 * time.Minute
	kineCompactIntervalJitter = 0
	kineCompactTimeout        = 5 * time.Second
	kineCompactMinRetain      = 1000
	kineCompactBatchSize      = 1000
	kinePollBatchSize         = 500
)

// Readiness probe constants. endpoint.Listen returns the unix-socket
// endpoint as soon as Kine is configured, but its gRPC server goroutine
// may not yet be accept()-ing connections. waitForKineReady bridges that
// gap by polling the socket with an etcd3 client until it responds, so
// that callers (controllers, informers, auto-registration) never dial a
// half-up socket.
const (
	// kineDialTimeout is the per-attempt dial timeout for the etcd3
	// health-check client.
	kineDialTimeout = 2 * time.Second
	// kinePollInterval is the delay between readiness probes.
	kinePollInterval = 500 * time.Millisecond
	// kineReadyTimeout is the overall deadline for Kine to become
	// ready. It bounds New()'s blocking on a backend that never
	// connects (e.g. unreachable PostgreSQL) so the caller gets a
	// timely error instead of hanging forever.
	kineReadyTimeout = 30 * time.Second
	// kineHealthCheckKey is the etcd3 key fetched by the readiness
	// probe. The value is irrelevant — a successful Get proves the
	// gRPC server is accepting connections and the backend is wired.
	kineHealthCheckKey = "health-check"
)

// startKine spawns an in-process Kine endpoint that speaks the etcd3 client
// protocol on a private, per-instance unix socket, translating it into SQL
// (or another Kine-supported dialect) traffic against dbEndpoint.
//
// Unlike pkg/kine/server.go's StartKine (which drives Kine's CLI wrapper
// through an OS-signal-driven context that only reacts to process-wide
// signals), this calls github.com/k3s-io/kine/pkg/endpoint.Listen directly:
// canceling ctx cleanly stops the spawned Kine endpoint, and wg.Wait() (once
// ctx is canceled) blocks until it has actually finished, with no subprocess
// involved.
//
//nolint:nonamedreturns // rerr is read from the defer below to clean up tmpDir on any error path.
func startKine(ctx context.Context,
	dbEndpoint string,
	kineWaitGroup *sync.WaitGroup,
) (endpoints []string, cleanup func(), rerr error) {
	tmpDir, err := os.MkdirTemp("", "libkapi-kine-")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp dir for kine socket: %w", err)
	}

	cleanupFn := func() {
		_ = os.RemoveAll(tmpDir)
	}

	defer func() {
		if rerr != nil {
			cleanupFn()
		}
	}()

	listenAddr := "unix://" + filepath.Join(tmpDir, kineSocketFileName)

	etcdConfig, err := endpoint.Listen(ctx, endpoint.Config{
		Listener:              listenAddr,
		Endpoint:              dbEndpoint,
		WaitGroup:             kineWaitGroup,
		NotifyInterval:        kineNotifyInterval,
		EmulatedETCDVersion:   kineEmulatedETCDVersion,
		CompactInterval:       kineCompactInterval,
		CompactIntervalJitter:  kineCompactIntervalJitter,
		CompactTimeout:        kineCompactTimeout,
		CompactMinRetain:       kineCompactMinRetain,
		CompactBatchSize:       kineCompactBatchSize,
		PollBatchSize:          kinePollBatchSize,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start embedded kine endpoint: %w", err)
	}

	return etcdConfig.Endpoints, cleanupFn, nil
}

// waitForKineReady blocks until the embedded Kine endpoint is accepting
// etcd3 client connections on endpoints, or readyCtx expires. It probes
// the socket by creating a throwaway etcd3 client and issuing a Get; a
// successful response means Kine's gRPC server is live and its backend
// is wired.
//
// The parent ctx is wrapped in a WithTimeout so that callers passing
// context.Background (no deadline) still get a bounded wait; callers that
// already carry a shorter deadline are respected.
func waitForKineReady(ctx context.Context, endpoints []string) error {
	logger := slog.Default().With("component", "storage")

	readyCtx, cancel := context.WithTimeout(ctx, kineReadyTimeout)
	defer cancel()

	for {
		ready, err := probeKineEndpoint(readyCtx, endpoints)
		if ready {
			return nil
		}

		logger.Info("Waiting for kine to be ready", "error", err)

		select {
		case <-readyCtx.Done():
			return fmt.Errorf("%w: %w", ErrKineNotReady, readyCtx.Err())
		case <-time.After(kinePollInterval):
		}
	}
}

// probeKineEndpoint issues a single etcd3 Get against endpoints to
// determine whether Kine's gRPC server is accepting connections. It
// returns (true, nil) on success and (false, err) on any failure (dial,
// Get, or close).
func probeKineEndpoint(ctx context.Context, endpoints []string) (bool, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: kineDialTimeout,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to create etcd3 health-check client: %w", err)
	}

	_, err = cli.Get(ctx, kineHealthCheckKey)
	_ = cli.Close()

	if err != nil {
		return false, fmt.Errorf("failed to probe kine endpoint: %w", err)
	}

	return true, nil
}

// prewarmFeatureCheck pre-caches the kine endpoint in the apiserver's
// global FeatureSupportChecker so that the apiserver's own
// CheckClient calls — fired later from each etcd3 storage client
// during buildDelegationChain — skip their background goroutine
// entirely (the endpoint is already in checkingEndpoint).
//
// Without this pre-warm, each etcd3 client the apiserver creates
// (CRD server, generic server, aggregator — one per API group)
// spawns a goroutine that calls c.Status(ctx, endpoint) to check
// whether the etcd server supports RequestWatchProgress. Those
// goroutines use the etcd3 client's internal context (c.Ctx()),
// which is canceled when the client is closed during shutdown.
// The cancellation produces a spurious
//   "Failed to check if RequestWatchProgress is supported by etcd
//    after retrying"
// error at shutdown.
//
// By calling CheckClient ourselves with context.Background (never
// canceled), the goroutine we spawn succeeds cleanly and exits
// without error. The endpoint is cached, so the apiserver's later
// calls skip. The client we create here must outlive the goroutine
// (which completes in milliseconds since kine is already ready);
// it is closed via the returned cleanup function during shutdown,
// long after the goroutine has exited.
//
// If the Status RPC fails (e.g., kine's SQLite backend doesn't
// implement the dbstat table), the pre-warm is skipped — the
// apiserver will do its own check (the existing behavior).
func prewarmFeatureCheck(ctx context.Context, endpoints []string) (func(), error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: kineDialTimeout,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create feature-check client: %w", err)
	}

	// Verify the Status RPC works before calling CheckClient. Kine's
	// SQLite backend doesn't implement the dbstat table the Status
	// RPC needs, so c.Status returns an error. If we called
	// CheckClient with context.Background (never canceled) anyway,
	// the goroutine would retry forever, leaking resources.
	statusCtx, cancel := context.WithTimeout(ctx, kineDialTimeout)

	_, statusErr := cli.Status(statusCtx, endpoints[0])

	cancel()

	if statusErr != nil {
		_ = cli.Close()

		logger := slog.Default().With("component", "storage")
		logger.Debug("Skipping RequestWatchProgress feature pre-warm", "error", statusErr)

		return func() {}, nil
	}

	// Use context.Background for CheckClient: the goroutine must
	// outlive the caller's context so it isn't canceled on shutdown.
	//nolint:contextcheck // intentional — goroutine must not be canceled by caller's context
	feature.DefaultFeatureSupportChecker.CheckClient(
		context.Background(), cli, apiserverstorage.RequestWatchProgress)

	return func() { _ = cli.Close() }, nil
}
