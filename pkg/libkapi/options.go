package libkapi

import (
	"context"
	"log/slog"

	"k8s.io/apimachinery/pkg/runtime"
)

// Option configures a Server. Passed to New as a variadic list, applied in
// order; the last call for a given setting wins. The context is passed so
// options that require I/O (e.g. OIDC discovery, via the auth-wrapping
// options in authoptions.go) can use it.
type Option func(ctx context.Context, cfg *config) error

// WithAddr sets the listener address, e.g. ":8080". If never set, New
// defaults to ":"+$PORT, falling back to ":8080" if PORT is unset or invalid.
func WithAddr(addr string) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.addr = addr

		return nil
	}
}

// WithStorage sets the polymorphic connection string used to reach or start
// the storage backend, e.g. "postgres://...", "mysql://...",
// "sqlite://path.db", "etcd://host:2379", or "unix:///path/to/kine.sock".
func WithStorage(storage string) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.storage = storage

		return nil
	}
}

// WithLogger sets the logger that receives libkapi's internal log output. If
// never set, slog.Default() is used.
//
// This is the single logging entry point: all log output — libkapi's own
// messages and klog output from the embedded Kubernetes packages (apiserver,
// apiextensions-apiserver, kube-aggregator, client-go) — is routed through
// it. New bridges klog to it automatically via logging.InstallKlogAdapter,
// so the
// caller never needs to configure klog separately.
func WithLogger(logger *slog.Logger) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.logger = logger

		return nil
	}
}

// WithTLS is reserved for future use: passing it causes New to return
// ErrNotImplemented. It exists now so the public API will not need a
// breaking change once TLS support is added.
func WithTLS(tls TLSConfig) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.tls = &tls

		return nil
	}
}

// WithScheme lets the caller register additional types beyond the standard
// API groups libkapi wires by default.
func WithScheme(scheme *runtime.Scheme) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.scheme = scheme

		return nil
	}
}
