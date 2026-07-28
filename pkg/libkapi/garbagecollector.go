package libkapi

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/controller-manager/pkg/informerfactory"
	"k8s.io/kubernetes/pkg/controller/garbagecollector"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// defaultGCWorkers is used when GarbageCollectorConfig.Workers is zero.
	// Matches the upstream kube-controller-manager default.
	defaultGCWorkers = 5

	// defaultGCSyncPeriod is used when GarbageCollectorConfig.SyncPeriod is
	// zero. Matches the upstream kube-controller-manager default.
	defaultGCSyncPeriod = 30 * time.Second

	// defaultGCInitialSyncTimeout is used when
	// GarbageCollectorConfig.InitialSyncTimeout is zero. Matches the upstream
	// kube-controller-manager default.
	defaultGCInitialSyncTimeout = 60 * time.Second

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
	gcUserAgent = "libkapi-garbage-collector"

	// gcQPSMultiplier doubles the rest client throughput for the garbage
	// collector because each object deletion takes two API calls. Matches the
	// upstream kube-controller-manager behaviour.
	gcQPSMultiplier = 2
)

// GarbageCollectorConfig configures the embedded ownerReferences-based
// garbage collector, added via WithGarbageCollector. A zero value for any
// field falls back to the upstream kube-controller-manager default.
type GarbageCollectorConfig struct {
	// Workers is the number of concurrent attempt-to-delete/attempt-to-orphan
	// workers. Defaults to 5 if zero.
	Workers int

	// SyncPeriod is how often the collector resyncs with the API server's
	// discovery information so newly added GVRs (e.g. from a freshly
	// installed CRD) are watched. Defaults to 30s if zero.
	SyncPeriod time.Duration

	// InitialSyncTimeout bounds how long the collector waits for its
	// informers to sync once, at startup, before giving up and running
	// anyway. Defaults to 60s if zero.
	InitialSyncTimeout time.Duration
}

// WithGarbageCollector registers upstream Kubernetes' ownerReferences
// garbage collector: it watches every deletable resource the API server
// advertises via discovery and deletes dependents when their owner is
// removed.
//
// This is deliberately opt-in, not automatic. The upstream collector is a
// genuinely heavy dependency: it opens a discovery client plus a dynamic
// informer and a metadata informer for every resource type the server
// advertises, and runs its own REST-mapper reset loop on top of that.
// Servers that only ever need to garbage-collect a small, fixed set of
// resource types are almost always better served by a purpose-built
// reconciler for just those types (cheaper, and its behaviour is easier to
// reason about) than by pulling in the general-purpose collector this option
// wires up.
//
// It needs no other option: WithGarbageCollector registers a Controller of
// its own, which is what makes libkapi build and start a Manager at all.
func WithGarbageCollector(cfg GarbageCollectorConfig) Option {
	return func(_ context.Context, serverCfg *config) error {
		if cfg.Workers <= 0 {
			cfg.Workers = defaultGCWorkers
		}

		if cfg.SyncPeriod <= 0 {
			cfg.SyncPeriod = defaultGCSyncPeriod
		}

		if cfg.InitialSyncTimeout <= 0 {
			cfg.InitialSyncTimeout = defaultGCInitialSyncTimeout
		}

		serverCfg.controllers = append(serverCfg.controllers, &garbageCollectorController{cfg: cfg})

		return nil
	}
}

// garbageCollectorController is the Controller WithGarbageCollector
// registers. It only carries the config and registers a
// garbageCollectorRunner with the Manager - the actual clients, informers,
// and GarbageCollector are built lazily, in the runner's Start, so
// construction uses the Manager's own running context (see
// garbageCollectorRunner.Start) instead of one captured at setup time.
type garbageCollectorController struct {
	// cfg is the caller's config with every zero field already resolved to
	// its default by WithGarbageCollector.
	cfg GarbageCollectorConfig
}

func (g *garbageCollectorController) SetupWithManager(mgr Manager) error {
	runner := &garbageCollectorRunner{
		restConfig: mgr.GetConfig(),
		cfg:        g.cfg,
	}

	err := mgr.Add(runner)
	if err != nil {
		return fmt.Errorf("failed to register garbage collector with manager: %w", err)
	}

	return nil
}

// garbageCollectorRunner orchestrates the garbage collector lifecycle as a
// controller-runtime manager.Runnable.
type garbageCollectorRunner struct {
	restConfig *restclient.Config
	cfg        GarbageCollectorConfig
}

// Compile-time assertion that the runner implements manager.Runnable.
var _ manager.Runnable = (*garbageCollectorRunner)(nil)

// Start builds the typed, metadata, and discovery clients, the REST mapper,
// the shared and metadata informer factories, and the GarbageCollector
// itself - using ctx, the Manager's own running context, rather than one
// captured when the Controller was set up - then runs the collector's
// workers, its discovery resync loop, and the REST mapper refresh loop until
// ctx is cancelled. It blocks until all background goroutines have exited.
func (r *garbageCollectorRunner) Start(ctx context.Context) error {
	clients, err := newGCClients(r.restConfig)
	if err != nil {
		return err
	}

	typedInformers := informers.NewSharedInformerFactory(clients.kube, gcInformerResyncPeriod)
	metadataInformers := metadatainformer.NewSharedInformerFactory(clients.metadata, gcInformerResyncPeriod)
	informerFactory := informerfactory.NewInformerFactory(typedInformers, metadataInformers)

	// informersStarted is closed once both informer factories have been
	// started; the GraphBuilder uses it to know when its monitors may begin
	// syncing.
	informersStarted := make(chan struct{})

	collector, err := garbagecollector.NewGarbageCollector(
		ctx,
		clients.kube,
		clients.metadata,
		clients.restMapper,
		garbagecollector.DefaultIgnoredResources(),
		informerFactory,
		informersStarted,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrGarbageCollectorInit, err)
	}

	// Reset the REST mapper periodically so new CRDs and aggregated
	// resources are picked up by both the mapper and the garbage collector.
	go runGCRESTMapperReset(ctx, clients.restMapper, gcRESTMapperResetPeriod)

	// Start the garbage collector workers. collector.Run blocks until ctx.Done().
	go collector.Run(ctx, r.cfg.Workers, r.cfg.InitialSyncTimeout)

	// Periodically resync the garbage collector with the API server's
	// discovery information so new GVRs are watched.
	go collector.Sync(ctx, clients.discovery, r.cfg.SyncPeriod)

	// Start informers after the GC goroutines so the GraphBuilder is ready
	// to receive events, then signal the GraphBuilder that informers are
	// running.
	typedInformers.Start(ctx.Done())
	metadataInformers.Start(ctx.Done())
	close(informersStarted)

	<-ctx.Done()

	return nil
}

// gcClients bundles the clients and REST mapper the garbage collector needs,
// so newGCClients can return them as a single value instead of a long,
// same-typed-adjacent return list.
type gcClients struct {
	kube       kubernetes.Interface
	metadata   metadata.Interface
	discovery  discovery.DiscoveryInterface
	restMapper meta.ResettableRESTMapper
}

// newGCClients builds the typed, metadata, and discovery clients (all using
// restConfig tuned via configForGC) plus the deferred discovery REST mapper
// backing them.
func newGCClients(restConfig *restclient.Config) (*gcClients, error) {
	cfg := configForGC(restConfig)

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: typed client: %w", ErrGarbageCollectorClientBuild, err)
	}

	metadataClient, err := metadata.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata client: %w", ErrGarbageCollectorClientBuild, err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery client: %w", ErrGarbageCollectorClientBuild, err)
	}

	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	return &gcClients{
		kube:       kubeClient,
		metadata:   metadataClient,
		discovery:  discoveryClient,
		restMapper: restMapper,
	}, nil
}

// runGCRESTMapperReset resets restMapper every period until ctx is done.
func runGCRESTMapperReset(ctx context.Context, restMapper meta.ResettableRESTMapper, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			restMapper.Reset()
		}
	}
}

// configForGC returns a shallow copy of restConfig with the garbage
// collector User-Agent and a doubled QPS budget. The QPS bump matches
// upstream kube-controller-manager because each object deletion costs two
// API calls.
func configForGC(restConfig *restclient.Config) *restclient.Config {
	cfg := restclient.CopyConfig(restConfig)
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
			restclient.DefaultQPS*gcQPSMultiplier,
			restclient.DefaultBurst*gcQPSMultiplier,
		)
	}

	return cfg
}
