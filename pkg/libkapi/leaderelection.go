package libkapi

import "context"

// LeaderElectionConfig configures manager-wide leader election, backed by a
// coordination.k8s.io/v1 Lease — the same default resource lock
// controller-runtime itself uses. When enabled, only the elected replica's
// Controllers run their reconcile loops and non-webhook Runnables; the rest
// block in mgr.Start until they win an election or the process exits.
// Webhook handlers are unaffected either way.
type LeaderElectionConfig struct {
	// ID names the Lease object contenders coordinate on. Required.
	ID string

	// Namespace is the Lease's namespace. Defaults to the resolved system
	// namespace (see WithSystemNamespace, itself "libkapi" if unset) if
	// empty. ListenAndServe's ensureLeaderElectionNamespace step creates
	// this namespace if it doesn't already exist.
	Namespace string
}

// resolvedNamespace returns cfg.Namespace, or systemNamespace if unset. The
// single source of truth shared by buildManager (which points the Lease at
// this namespace) and Server.ensureLeaderElectionNamespace (which creates
// it), so the two can never disagree about where the Lease lives.
func (cfg *LeaderElectionConfig) resolvedNamespace(systemNamespace string) string {
	if cfg.Namespace != "" {
		return cfg.Namespace
	}

	return systemNamespace
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
