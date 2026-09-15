package libkapi

import "context"

// defaultSystemNamespace is used when WithSystemNamespace is never called.
const defaultSystemNamespace = "libkapi"

// WithSystemNamespace sets the namespace all of libkapi's own
// system-managed Secrets live in: the webhook serving certificate, the SA
// signing key, the SA token-rotation lister, and (unless overridden by
// LeaderElectionConfig.Namespace) the leader-election Lease. Defaults to
// "libkapi" if never called.
func WithSystemNamespace(ns string) Option {
	return func(_ context.Context, c *config) error {
		c.systemNamespace = ns

		return nil
	}
}
