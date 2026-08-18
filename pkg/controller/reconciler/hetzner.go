package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	caph_controllers "github.com/syself/cluster-api-provider-hetzner/controllers"
	sshclient "github.com/syself/cluster-api-provider-hetzner/pkg/services/baremetal/client/ssh"
	hcloudclient "github.com/syself/cluster-api-provider-hetzner/pkg/services/hcloud/client"
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// hetznerRateLimitWaitTime is how long CAPH reconcilers wait once the
	// Hetzner API reports rate-limit exhaustion. Hetzner allows 3600 requests
	// per hour per project, replenishing one request per second, so a 5-minute
	// pause (the CAPH upstream default) restores a meaningful budget.
	hetznerRateLimitWaitTime = 5 * time.Minute

	// hetznerRateLimiterBaseDelay is the initial delay for the exponential
	// backoff when requeuing failed Hetzner reconciliation requests.
	hetznerRateLimiterBaseDelay = 30 * time.Second

	// hetznerRateLimiterMaxDelay caps the exponential backoff, aligned with
	// hetznerRateLimitWaitTime so requeues do not outpace the API budget
	// recovery.
	hetznerRateLimiterMaxDelay = hetznerRateLimitWaitTime

	// hetznerRateLimiterBucketRate is the per-controller token-bucket refill
	// rate for Hetzner reconciliations, damping steady-state reconcile churn.
	hetznerRateLimiterBucketRate = rate.Limit(0.1)

	// hetznerRateLimiterBucketBurst is the burst size for the per-controller
	// token-bucket limiter.
	hetznerRateLimiterBucketBurst = 1
)

type hetznerModule struct{}

// NewHetznerModule creates a new module for Hetzner (CAPH) CAPI.
func NewHetznerModule() Module {
	return &hetznerModule{}
}

// Name returns the name of the module.
func (m *hetznerModule) Name() config.Provider {
	return config.ProviderHetzner
}

// Setup sets up the Hetzner CAPI controllers.
func (m *hetznerModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupHetzner(ctx, deps.Manager, deps.Options)
}

func setupHetzner(ctx context.Context, manager ctrl.Manager, base ctrlcontroller.Options) error {
	logger := logging.FromContext(ctx)
	hcloudClientFactory := hcloudclient.NewFactory()

	logger.Info("Setting up HetznerCluster controller")

	err := setupHetznerClusterWithManager(
		ctx,
		manager,
		newHetznerControllerOptions(base),
		hcloudClientFactory,
	)
	if err != nil {
		return fmt.Errorf("failed to setup HetznerCluster controller: %w", err)
	}

	logger.Info("Setting up HCloudMachine controller")

	err = setupHCloudMachineWithManager(
		ctx,
		manager,
		newHetznerControllerOptions(base),
		hcloudClientFactory,
	)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudMachine controller: %w", err)
	}

	logger.Info("Setting up HCloudMachineTemplate controller")

	err = setupHCloudMachineTemplateWithManager(
		ctx,
		manager,
		newHetznerControllerOptions(base),
		hcloudClientFactory,
	)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudMachineTemplate controller: %w", err)
	}

	logger.Info("Setting up HCloudRemediation controller")

	err = setupHCloudRemediationWithManager(
		ctx,
		manager,
		newHetznerControllerOptions(base),
		hcloudClientFactory,
	)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudRemediation controller: %w", err)
	}

	return nil
}

func setupHetznerClusterWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt ctrlcontroller.Options,
	hcloudClientFactory hcloudclient.Factory,
) error {
	err := (&caph_controllers.HetznerClusterReconciler{
		Client:              manager.GetClient(),
		APIReader:           manager.GetAPIReader(),
		RateLimitWaitTime:   hetznerRateLimitWaitTime,
		HCloudClientFactory: hcloudClientFactory,
		// CAPH waits on this group to drain per-workload-cluster target
		// managers after manager shutdown. Kommodity has no post-manager
		// teardown hook, so the goroutines stop at process exit.
		TargetClusterManagersWaitGroup: &sync.WaitGroup{},
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup HetznerCluster reconciler: %w", err)
	}

	return nil
}

func setupHCloudMachineWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt ctrlcontroller.Options,
	hcloudClientFactory hcloudclient.Factory,
) error {
	err := (&caph_controllers.HCloudMachineReconciler{
		Client:              manager.GetClient(),
		APIReader:           manager.GetAPIReader(),
		RateLimitWaitTime:   hetznerRateLimitWaitTime,
		HCloudClientFactory: hcloudClientFactory,
		SSHClientFactory:    sshclient.NewFactory(),
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudMachine reconciler: %w", err)
	}

	return nil
}

func setupHCloudMachineTemplateWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt ctrlcontroller.Options,
	hcloudClientFactory hcloudclient.Factory,
) error {
	err := (&caph_controllers.HCloudMachineTemplateReconciler{
		Client:              manager.GetClient(),
		APIReader:           manager.GetAPIReader(),
		RateLimitWaitTime:   hetznerRateLimitWaitTime,
		HCloudClientFactory: hcloudClientFactory,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudMachineTemplate reconciler: %w", err)
	}

	return nil
}

func setupHCloudRemediationWithManager(
	ctx context.Context,
	manager ctrl.Manager,
	opt ctrlcontroller.Options,
	hcloudClientFactory hcloudclient.Factory,
) error {
	err := (&caph_controllers.HCloudRemediationReconciler{
		Client:              manager.GetClient(),
		APIReader:           manager.GetAPIReader(),
		RateLimitWaitTime:   hetznerRateLimitWaitTime,
		HCloudClientFactory: hcloudClientFactory,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup HCloudRemediation reconciler: %w", err)
	}

	return nil
}

// newHetznerControllerOptions takes the base controller options and overrides
// the RateLimiter with a Hetzner-specific one: a per-item exponential backoff
// combined with a token bucket, both private to this controller.
//
// Coarse admission control on reconcile starts. One reconcile issues many
// Hetzner API calls, so this does not track the per-project budget of 3600
// requests per hour; CAPH's RateLimitWaitTime handles API exhaustion.
//
// The bucket is per-controller: TypedMaxOfRateLimiter.When calls When on every
// sub-limiter, and TypedBucketRateLimiter.When reserves a token even when the
// exponential backoff wins the max, so one bucket shared across controllers
// would drain on requeues it never delays.
func newHetznerControllerOptions(base ctrlcontroller.Options) ctrlcontroller.Options {
	opt := base
	opt.RateLimiter = workqueue.NewTypedMaxOfRateLimiter[reconcile.Request](
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
			hetznerRateLimiterBaseDelay,
			hetznerRateLimiterMaxDelay,
		),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{
			Limiter: rate.NewLimiter(hetznerRateLimiterBucketRate, hetznerRateLimiterBucketBurst),
		},
	)

	return opt
}
