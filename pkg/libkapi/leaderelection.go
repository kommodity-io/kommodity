package libkapi

import "context"

// defaultLeaderElectionNamespace is used when LeaderElectionConfig.Namespace
// is empty - the one namespace libkapi's own bootstrap-default-namespace
// post-start hook guarantees exists.
const defaultLeaderElectionNamespace = "default"

// LeaderElectionConfig configures manager-wide leader election, backed by a
// coordination.k8s.io/v1 Lease — the same default resource lock
// controller-runtime itself uses. When enabled, only the elected replica's
// Controllers run their reconcile loops and non-webhook Runnables; the rest
// block in mgr.Start until they win an election or the process exits.
// Webhook handlers are unaffected either way.
type LeaderElectionConfig struct {
	// ID names the Lease object contenders coordinate on. Required.
	ID string

	// Namespace is the Lease's namespace. Defaults to "default" if empty.
	Namespace string
}

// WithLeaderElection enables manager-wide leader election using the
// server's own privileged loopback identity to talk to its own Lease
// object. Without this option (today's only behavior), every registered
// Controller runs unmodified on every replica — they must be written to
// tolerate that.
func WithLeaderElection(cfg LeaderElectionConfig) Option {
	return func(_ context.Context, c *config) error {
		if cfg.ID == "" {
			return ErrLeaderElectionIDRequired
		}

		c.leaderElection = &cfg

		return nil
	}
}
