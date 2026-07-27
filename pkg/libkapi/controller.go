package libkapi

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Controller registers reconcilers and/or runnables against the Manager
// libkapi builds, owns, and runs for the life of the Server, using the
// server's own privileged (system:masters-equivalent) loopback identity —
// the same one pkg/libkapi/controllers uses internally. SetupWithManager is
// called once per Controller, synchronously, during New — before the
// server starts serving. A returned error fails New. Implementations
// typically call ctrl.NewControllerManagedBy(mgr).For(&MyType{}).Complete(r)
// and/or mgr.Add(myRunnable) here. MyType's GVK must already be registered
// via WithScheme.
type Controller interface {
	SetupWithManager(mgr Manager) error
}

// Manager re-exports controller-runtime's Manager so callers implementing
// Controller don't need to import sigs.k8s.io/controller-runtime directly
// for this one type — matches the Authorizer re-export pattern in
// authoptions.go.
type Manager = ctrl.Manager

// WithController registers a Controller. Can be passed more than once; each
// is set up in the order given.
func WithController(c Controller) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.controllers = append(cfg.controllers, c)

		return nil
	}
}
