package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/controller-manager/pkg/informerfactory"
	"k8s.io/kubernetes/pkg/controller/garbagecollector"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// gcInformerResyncPeriod is the resync period for the typed and metadata
	// informer factories backing the garbage collector. Matches the upstream
	// kube-controller-manager default.
	gcInformerResyncPeriod = 12 * time.Hour

	// gcRESTMapperResetPeriod is how often the deferred discovery REST mapper
	// is reset to pick up new resources. Matches the upstream
	// kube-controller-manager default.
	gcRESTMapperResetPeriod = 30 * time.Second

	// gcUserAgent is the User-Agent string used by the garbage collector's
	// rest clients.
	gcUserAgent = "kommodity-garbage-collector"

	// gcQPSMultiplier doubles the rest client throughput for the garbage
	// collector because each object deletion takes two API calls. Matches the
	// upstream kube-controller-manager behaviour.
	gcQPSMultiplier = 2
)

// gcDeps groups the inputs required to set up the garbage collector. The
// dependencies are extracted into a struct to avoid a long argument list.
type gcDeps struct {
	manager    ctrl.Manager
	restConfig *rest.Config
	gcConfig   *config.GarbageCollectorConfig
}

// setupGarbageCollector wires the upstream Kubernetes ownerReferences
// garbage collector into the controller manager. The collector watches every
// deletable resource the API server advertises via discovery and deletes
// dependents when their owner is removed.
//
// If gcConfig is nil or disabled, the function is a no-op.
func setupGarbageCollector(ctx context.Context, deps gcDeps) error {
	logger := logging.FromContext(ctx)

	if deps.gcConfig == nil || !deps.gcConfig.Enabled {
		logger.Info("Garbage collector is disabled, skipping setup")

		return nil
	}

	logger.Info("Setting up garbage collector",
		zap.Int("workers", deps.gcConfig.Workers),
		zap.Duration("syncPeriod", deps.gcConfig.SyncPeriod),
		zap.Duration("initialSyncTimeout", deps.gcConfig.InitialSyncTimeout))

	runner, err := newGarbageCollectorRunner(ctx, deps)
	if err != nil {
		return err
	}

	err = deps.manager.Add(runner)
	if err != nil {
		return fmt.Errorf("failed to register garbage collector with manager: %w", err)
	}

	return nil
}

// garbageCollectorRunner orchestrates the garbage collector lifecycle as a
// controller-runtime manager.Runnable. It owns the typed, metadata, and
// discovery clients, the REST mapper, the shared and metadata informer
// factories, and the GarbageCollector itself.
type garbageCollectorRunner struct {
	gc                 *garbagecollector.GarbageCollector
	discoveryClient    discovery.DiscoveryInterface
	restMapper         meta.ResettableRESTMapper
	typedInformers     informers.SharedInformerFactory
	metadataInformers  metadatainformer.SharedInformerFactory
	informersStarted   chan struct{}
	workers            int
	syncPeriod         time.Duration
	initialSyncTimeout time.Duration
}

func newGarbageCollectorRunner(
	ctx context.Context,
	deps gcDeps,
) (*garbageCollectorRunner, error) {
	gcConfig := configForGC(deps.restConfig)

	kubeClient, err := kubernetes.NewForConfig(gcConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: typed client: %w", ErrGarbageCollectorClientBuild, err)
	}

	metadataClient, err := metadata.NewForConfig(gcConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata client: %w", ErrGarbageCollectorClientBuild, err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(gcConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery client: %w", ErrGarbageCollectorClientBuild, err)
	}

	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	typedInformers := informers.NewSharedInformerFactory(kubeClient, gcInformerResyncPeriod)
	metadataInformers := metadatainformer.NewSharedInformerFactory(metadataClient, gcInformerResyncPeriod)
	informerFactory := informerfactory.NewInformerFactory(typedInformers, metadataInformers)

	// informersStarted is closed by the runner once both informer factories
	// have been started; the GraphBuilder uses it to know when its monitors
	// may begin syncing.
	informersStarted := make(chan struct{})

	collector, err := garbagecollector.NewGarbageCollector(
		ctx,
		kubeClient,
		metadataClient,
		restMapper,
		garbagecollector.DefaultIgnoredResources(),
		informerFactory,
		informersStarted,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGarbageCollectorInit, err)
	}

	return &garbageCollectorRunner{
		gc:                 collector,
		discoveryClient:    discoveryClient,
		restMapper:         restMapper,
		typedInformers:     typedInformers,
		metadataInformers:  metadataInformers,
		informersStarted:   informersStarted,
		workers:            deps.gcConfig.Workers,
		syncPeriod:         deps.gcConfig.SyncPeriod,
		initialSyncTimeout: deps.gcConfig.InitialSyncTimeout,
	}, nil
}

// Compile-time assertion that the runner implements manager.Runnable.
var _ manager.Runnable = (*garbageCollectorRunner)(nil)

// Start runs the garbage collector workers, the discovery resync loop, and
// the REST mapper refresh loop until ctx is cancelled. It blocks until all
// background goroutines have exited.
func (r *garbageCollectorRunner) Start(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	logger.Info("Starting garbage collector runner")

	// Reset the REST mapper periodically so new CRDs and aggregated resources
	// are picked up by both the mapper and the garbage collector.
	go r.runRESTMapperReset(ctx, gcRESTMapperResetPeriod)

	// Start the garbage collector workers. gc.Run blocks until ctx.Done().
	go r.gc.Run(ctx, r.workers, r.initialSyncTimeout)

	// Periodically resync the garbage collector with the API server's
	// discovery information so new GVRs are watched.
	go r.gc.Sync(ctx, r.discoveryClient, r.syncPeriod)

	// Start informers after the GC goroutines so the GraphBuilder is ready
	// to receive events, then signal the GraphBuilder that informers are
	// running.
	r.typedInformers.Start(ctx.Done())
	r.metadataInformers.Start(ctx.Done())
	close(r.informersStarted)

	logger.Info("Garbage collector runner started")

	<-ctx.Done()
	logger.Info("Garbage collector runner stopped")

	return nil
}

func (r *garbageCollectorRunner) runRESTMapperReset(ctx context.Context, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.restMapper.Reset()
		}
	}
}

// configForGC returns a shallow copy of restConfig with the garbage collector
// User-Agent and a doubled QPS budget. The QPS bump matches upstream
// kube-controller-manager because each object deletion costs two API calls.
func configForGC(restConfig *rest.Config) *rest.Config {
	cfg := rest.CopyConfig(restConfig)
	cfg.UserAgent = gcUserAgent

	if cfg.QPS > 0 {
		cfg.QPS *= gcQPSMultiplier
		cfg.Burst *= gcQPSMultiplier
	} else if cfg.RateLimiter == nil {
		// When neither QPS nor a custom RateLimiter is set, client-go applies
		// its own default. Install an explicit rate limiter at the doubled
		// default so the garbage collector matches upstream behaviour
		// regardless of the parent config's limits.
		cfg.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(
			rest.DefaultQPS*gcQPSMultiplier,
			rest.DefaultBurst*gcQPSMultiplier,
		)
	}

	return cfg
}
