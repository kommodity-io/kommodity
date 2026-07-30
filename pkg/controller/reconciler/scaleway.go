package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	scaleway_capi_controller "github.com/scaleway/cluster-api-provider-scaleway/pkg/controller"
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// scalewayRateLimiterBaseDelay is the initial delay for the exponential
	// backoff when requeuing failed Scaleway reconciliation requests.
	scalewayRateLimiterBaseDelay = 30 * time.Second

	// scalewayRateLimiterMaxDelay caps the exponential backoff. A 2-minute
	// ceiling accommodates transient Scaleway API errors such as GPU stock
	// shortages without overwhelming the API.
	scalewayRateLimiterMaxDelay = 2 * time.Minute

	// scalewayRateLimiterBucketRate is the overall token-bucket refill rate
	// for Scaleway reconciliations.
	scalewayRateLimiterBucketRate = rate.Limit(0.1)

	// scalewayRateLimiterBucketBurst is the burst size for the overall
	// token-bucket limiter.
	scalewayRateLimiterBucketBurst = 1
)

type scalewayModule struct{}

// NewScalewayModule creates a new module for Scaleway CAPI.
func NewScalewayModule() Module {
	return &scalewayModule{}
}

// Name returns the name of the module.
func (m *scalewayModule) Name() config.Provider {
	return config.ProviderScaleway
}

// Setup sets up the Scaleway CAPI controllers.
func (m *scalewayModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupScaleway(ctx, deps.Manager, newScalewayControllerOptions(deps.Options))
}

func setupScaleway(ctx context.Context, manager ctrl.Manager, opt ctrlcontroller.Options) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ScalewayCluster controller")

	err := setupScalewayClusterWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayCluster controller: %w", err)
	}

	logger.Info("Setting up ScalewayMachine controller")

	err = setupScalewayMachineWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayMachine controller: %w", err)
	}

	return nil
}

// newScalewayControllerOptions takes the base controller options and overrides
// the RateLimiter with a Scaleway-specific one that better fits our retry needs
// (e.g. GPU stock shortage on Scaleway).
func newScalewayControllerOptions(base ctrlcontroller.Options) ctrlcontroller.Options {
	opt := base
	opt.RateLimiter = newScalewayRateLimiter()

	return opt
}

// newScalewayRateLimiter builds a multi-layer rate limiter for Scaleway
// reconciliations: per-item exponential backoff capped at
// scalewayRateLimiterMaxDelay, combined with an overall token-bucket limiter.
func newScalewayRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return workqueue.NewTypedMaxOfRateLimiter[reconcile.Request](
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
			scalewayRateLimiterBaseDelay,
			scalewayRateLimiterMaxDelay,
		),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{
			Limiter: rate.NewLimiter(scalewayRateLimiterBucketRate, scalewayRateLimiterBucketBurst),
		},
	)
}

func setupScalewayMachineWithManager(manager ctrl.Manager, opt ctrlcontroller.Options) error {
	err := scaleway_capi_controller.NewScalewayMachineReconciler(manager.GetClient()).
		SetupWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayMachine reconciler: %w", err)
	}

	return nil
}

func setupScalewayClusterWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt ctrlcontroller.Options,
) error {
	err := scaleway_capi_controller.NewScalewayClusterReconciler(manager.GetClient()).
		SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ScalewayCluster reconciler: %w", err)
	}

	return nil
}
