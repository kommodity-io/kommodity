package libkapi

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// isKineDSNScheme reports whether scheme is a connection-string scheme Kine's
// upstream driver dispatch understands (SQL dialects plus NATS), requiring an
// embedded Kine endpoint to translate it into the etcd3 client protocol.
func isKineDSNScheme(scheme string) bool {
	switch scheme {
	case "postgres", "postgresql", "mysql", "sqlite", "nats":
		return true
	default:
		return false
	}
}

// storageHandle is the resolved, running storage backend for a Server: the
// etcd3-compatible endpoints to hand to RESTOptionsGetters, plus however this
// backend needs to be torn down.
//
// close has its own lifecycle, independent of the context resolveStorage was
// called with: it must be safe to call on any error path, even one that runs
// long before (or without) the caller ever canceling their own context - an
// embedded Kine endpoint tied directly to the caller's context would
// otherwise deadlock a "clean up on error" call, since nothing would ever
// cancel that context to unblock the wait.
type storageHandle struct {
	endpoints []string
	close     func()
}

// resolveStorage dispatches cfg.Storage by URL scheme to the right backend:
//   - Kine DSN schemes (postgres/mysql/sqlite/nats) spawn an in-process Kine
//     endpoint via startKine, on its own cancelable context derived from ctx.
//   - "etcd" and "unix" talk directly to an already-running etcd3-compatible
//     endpoint; nothing is spawned, so close is a no-op.
func resolveStorage(ctx context.Context, connStr string) (*storageHandle, error) {
	if connStr == "" {
		return nil, ErrEmptyConnectionString
	}

	parsed, err := url.Parse(connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage connection string: %w", err)
	}

	switch {
	case isKineDSNScheme(parsed.Scheme):
		return resolveKineStorage(ctx, connStr)
	case parsed.Scheme == "etcd":
		if parsed.Host == "" {
			return nil, fmt.Errorf("%w: %q", ErrEmptyStorageEndpoint, connStr)
		}

		return &storageHandle{
			endpoints: []string{"http://" + parsed.Host},
			close:     func() {},
		}, nil
	case parsed.Scheme == "unix":
		if parsed.Path == "" {
			return nil, fmt.Errorf("%w: %q", ErrEmptyStorageEndpoint, connStr)
		}

		return &storageHandle{
			endpoints: []string{connStr},
			close:     func() {},
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedConnectionScheme, parsed.Scheme)
	}
}

func resolveKineStorage(ctx context.Context, connStr string) (*storageHandle, error) {
	kineCtx, cancel := context.WithCancel(ctx)

	var kineWaitGroup sync.WaitGroup

	endpoints, cleanup, err := startKine(kineCtx, connStr, &kineWaitGroup)
	if err != nil {
		cancel()

		return nil, err
	}

	return &storageHandle{
		endpoints: endpoints,
		close: func() {
			cancel()
			kineWaitGroup.Wait()
			cleanup()
		},
	}, nil
}
