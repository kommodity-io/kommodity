package storage

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// IsKineDSNScheme reports whether scheme is a connection-string scheme Kine's
// upstream driver dispatch understands (SQL dialects plus NATS), requiring an
// embedded Kine endpoint to translate it into the etcd3 client protocol.
func IsKineDSNScheme(scheme string) bool {
	switch scheme {
	case "postgres", "postgresql", "mysql", "sqlite", "nats":
		return true
	default:
		return false
	}
}

// Handle is the resolved, running storage backend for a Server: the
// etcd3-compatible endpoints to hand to RESTOptionsGetters, plus however this
// backend needs to be torn down.
//
// Close has its own lifecycle, independent of the context Resolve was called
// with: it must be safe to call on any error path, even one that runs long
// before (or without) the caller ever canceling their own context - an
// embedded Kine endpoint tied directly to the caller's context would
// otherwise deadlock a "clean up on error" call, since nothing would ever
// cancel that context to unblock the wait.
type Handle struct {
	endpoints []string
	close     func()
}

// Endpoints returns the etcd3-compatible endpoints this backend exposes.
func (h *Handle) Endpoints() []string {
	return h.endpoints
}

// Close stops the backend if Resolve spawned one (e.g. an in-process Kine
// endpoint), and is a no-op for backends that talk to an already-running
// endpoint (etcd://, unix://).
func (h *Handle) Close() {
	h.close()
}

// Resolve dispatches connStr by URL scheme to the right backend:
//   - Kine DSN schemes (postgres/mysql/sqlite/nats) spawn an in-process Kine
//     endpoint via startKine, on its own cancelable context derived from ctx.
//   - "etcd" and "unix" talk directly to an already-running etcd3-compatible
//     endpoint; nothing is spawned, so close is a no-op.
func Resolve(ctx context.Context, connStr string) (*Handle, error) {
	if connStr == "" {
		return nil, ErrEmptyConnectionString
	}

	parsed, err := url.Parse(connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage connection string: %w", err)
	}

	switch {
	case IsKineDSNScheme(parsed.Scheme):
		return resolveKineStorage(ctx, connStr)
	case parsed.Scheme == "etcd":
		if parsed.Host == "" {
			return nil, fmt.Errorf("%w: %q", ErrEmptyStorageEndpoint, connStr)
		}

		return &Handle{
			endpoints: []string{"http://" + parsed.Host},
			close:     func() {},
		}, nil
	case parsed.Scheme == "unix":
		if parsed.Path == "" {
			return nil, fmt.Errorf("%w: %q", ErrEmptyStorageEndpoint, connStr)
		}

		return &Handle{
			endpoints: []string{connStr},
			close:     func() {},
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedConnectionScheme, parsed.Scheme)
	}
}

func resolveKineStorage(ctx context.Context, connStr string) (*Handle, error) {
	kineCtx, cancel := context.WithCancel(ctx)

	var kineWaitGroup sync.WaitGroup

	endpoints, cleanup, err := startKine(kineCtx, connStr, &kineWaitGroup)
	if err != nil {
		cancel()

		return nil, err
	}

	// Block until Kine's gRPC server is accepting connections on the
	// unix socket. Without this gate, controllers and informers fired
	// by the apiserver's post-start hooks race the socket and produce
	// gRPC "dial unix …: operation was canceled" errors at startup.
	err = waitForKineReady(ctx, endpoints)
	if err != nil {
		cancel()
		kineWaitGroup.Wait()
		cleanup()

		return nil, err
	}

	// Pre-cache the kine endpoint in the apiserver's
	// FeatureSupportChecker so the apiserver's own CheckClient calls
	// skip their background goroutine. Without this, each etcd3 client
	// the apiserver creates spawns a goroutine that logs a spurious
	// "Failed to check if RequestWatchProgress is supported" error
	// when the client is closed during shutdown.
	featureCleanup, err := prewarmFeatureCheck(ctx, endpoints)
	if err != nil {
		cancel()
		kineWaitGroup.Wait()
		cleanup()

		return nil, fmt.Errorf("failed to pre-warm feature check: %w", err)
	}

	return &Handle{
		endpoints: endpoints,
		close: func() {
			featureCleanup()
			cancel()
			kineWaitGroup.Wait()
			cleanup()
		},
	}, nil
}
