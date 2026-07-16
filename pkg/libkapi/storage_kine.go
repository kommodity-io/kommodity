package libkapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/k3s-io/kine/pkg/endpoint"
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

	cleanup = func() {
		_ = os.RemoveAll(tmpDir)
	}

	defer func() {
		if rerr != nil {
			cleanup()
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
		CompactIntervalJitter: kineCompactIntervalJitter,
		CompactTimeout:        kineCompactTimeout,
		CompactMinRetain:      kineCompactMinRetain,
		CompactBatchSize:      kineCompactBatchSize,
		PollBatchSize:         kinePollBatchSize,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start embedded kine endpoint: %w", err)
	}

	return etcdConfig.Endpoints, cleanup, nil
}
