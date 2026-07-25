package libkapi

import (
	"context"

	restclient "k8s.io/client-go/rest"
)

// PostStartHookFunc runs once, in registration order, after ListenAndServe's
// listener is bound but before any registered Controller's Manager starts -
// the same timing window k8s.io/apiserver's own internal PostStartHook
// mechanism already uses for libkapi's own SA token controller and key
// persistence (see server.go's resolveAndSetAuth), adapted with two
// deliberate differences: hooks here run strictly in registration order
// (upstream dispatches all of its own hooks concurrently, in unspecified map
// order), and a hook's error fails ListenAndServe with an ordinary Go error
// instead of upstream's own klog.Fatal (see apiserver.go's
// newLoopbackClientConfig doc for the crash a failing post-start hook used
// to cause before the loopback client had a privileged identity).
//
// loopbackConfig is the server's own privileged (system:masters-equivalent)
// identity - the same one Controller/Manager use - so a hook can build
// whatever client it needs without requiring a full Controller/Manager for
// simple background work (e.g. a heartbeat loop).
type PostStartHookFunc func(ctx context.Context, loopbackConfig *restclient.Config) error

// WithPostStartHook registers fn to run once, after ListenAndServe's
// listener is bound and before the controller manager (if any) starts. Can
// be passed more than once; hooks run in registration order. An error from
// fn fails ListenAndServe.
func WithPostStartHook(fn PostStartHookFunc) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.postStartHooks = append(cfg.postStartHooks, fn)

		return nil
	}
}

// PreShutdownHookFunc runs once, in registration order, during Shutdown -
// after the controller manager (if any) has stopped, but before the API
// server's listener closes - so it still has a real chance to make one last
// privileged API call (e.g. deleting an object a PostStartHook created).
// Bounded by Shutdown's own ctx, the same window the controller manager's
// own stop gets.
type PreShutdownHookFunc func(ctx context.Context, loopbackConfig *restclient.Config) error

// WithPreShutdownHook registers fn to run once during Shutdown, before the
// listener closes. Can be passed more than once; hooks run in registration
// order. A hook's error is logged, not fatal to Shutdown - Shutdown must
// still finish tearing down the rest of the server regardless of any one
// cleanup step's failure.
func WithPreShutdownHook(fn PreShutdownHookFunc) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.preShutdownHooks = append(cfg.preShutdownHooks, fn)

		return nil
	}
}
