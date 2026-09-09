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
	"google.golang.org/grpc/keepalive"

	"github.com/k3s-io/kine/pkg/endpoint"
)

const kineSocketFileName = "kine.sock"

// endpointListen is a seam over endpoint.Listen so tests can simulate it
// panicking (see ErrKineStartPanicked) without a real prolonged database
// outage.
var endpointListen = endpoint.Listen //nolint:gochecknoglobals // test seam, not mutable config.

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

// gRPC keepalive tuning for Kine's embedded server. Left unset, endpoint.Listen
// builds its own grpc.Server using etcd's embed.DefaultGRPCKeepAliveMinTime
// (5s) as the KeepaliveEnforcementPolicy.MinTime - the minimum interval the
// server tolerates between PING frames from a client before it strikes it as
// abusive and, after a few strikes, sends GOAWAY(ENHANCE_YOUR_CALM,
// "too_many_pings").
//
// That 5s default is sized for a multi-tenant etcd server fielding pings from
// many independent, potentially untrusted clients. Here, the only clients are
// this same process's own etcd3 clients (one per API group, all dialing the
// same local unix socket), and the pings in question are grpc-go's automatic
// BDP (bandwidth-delay-product) pings - not user-configured keepalives -
// which each connection's transport sends on its own schedule as it receives
// stream data (e.g. watch events). With several such connections active
// during storage initialization, the 5s window is tight enough to trip on
// legitimate traffic: real signal in a multi-tenant deployment, false-positive
// churn here. Raising MinTime well above any plausible per-connection BDP
// ping cadence keeps the enforcement policy meaningful without punishing
// Kine's only, trusted callers.
const (
	kineGRPCKeepaliveMinTime = 30 * time.Second
	kineGRPCKeepaliveTime    = 2 * time.Hour
	kineGRPCKeepaliveTimeout = 20 * time.Second
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

// newKineGRPCServer builds the gRPC server startKine hands to Kine via
// Config.GRPCServer, in place of the one endpoint.Listen would otherwise
// build internally with etcd's own keepalive defaults. See the
// kineGRPCKeepalive* constants for why MinTime is raised.
func newKineGRPCServer() *grpc.Server {
	return grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             kineGRPCKeepaliveMinTime,
			PermitWithoutStream: false,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    kineGRPCKeepaliveTime,
			Timeout: kineGRPCKeepaliveTimeout,
		}),
	)
}

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

	// endpointListen can panic instead of returning an error - see
	// ErrKineStartPanicked - if the database stays unreachable for its
	// entire internal retry window. Recover so a prolonged outage surfaces
	// as a normal error instead of crashing the process.
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				rerr = fmt.Errorf("%w: %w", ErrKineStartPanicked, recoveredErr)
			} else {
				rerr = fmt.Errorf("%w: %v", ErrKineStartPanicked, recovered)
			}
		}
	}()

	listenAddr := "unix://" + filepath.Join(tmpDir, kineSocketFileName)

	etcdConfig, err := endpointListen(ctx, endpoint.Config{
		GRPCServer:            newKineGRPCServer(),
		Listener:              listenAddr,
		Endpoint:              dbEndpoint,
		WaitGroup:             kineWaitGroup,
		NotifyInterval:        kineNotifyInterval,
		EmulatedETCDVersion:   kineEmulatedETCDVersion,
		CompactInterval:       kineCompactInterval,
		CompactIntervalJitter: kineCompactIntervalJitter,
		CompactTimeout:        kineCompactTimeout,
		CompactMinRetain:      kineCompactMinRetain,
		CompactBatchSize:      kineCompactBatchSize,
		PollBatchSize:         kinePollBatchSize,
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
