package reconciler

import (
	"context"

	"github.com/kommodity-io/kommodity/pkg/config"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// SetupDeps bundles runtime dependencies that modules need.
// Each Module can choose to ignore fields it doesn't use.
type SetupDeps struct {
	Manager      ctrl.Manager
	ClusterCache clustercache.ClusterCache
	Options      controller.Options
}

// Module is a pluggable controller installer.
// Implementations perform their own Setup and return an error on failure.
type Module interface {
	Name() config.Provider
	Setup(ctx context.Context, deps SetupDeps) error
}

// Factory builds the list of Modules to set up based on config & environment.
type Factory interface {
	Build(cfg *config.KommodityConfig) (map[config.Provider][]Module, error)
}
