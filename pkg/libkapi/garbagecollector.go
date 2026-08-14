package libkapi

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
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
	"sigs.k8s.io/controller-runtime/pkg/webhook"
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

	// gcWebhookReadyTimeout bounds how long Start waits, when a webhook
	// server was configured via WithWebhookServer, for that server's TLS
	// listener to become dialable before giving up. controller-runtime's
	// Manager.Start launches webhook servers before any other runnable
	// specifically to avoid a race between conversion webhooks and cache
	// sync (see WithGarbageCollector's doc), but that only guarantees launch
	// order, not readiness: a webhook.Server signals "ready" to the manager
	// the instant its goroutine is scheduled - well before it actually
	// finishes provisioning its certificate and binding its TLS listener.
	// Without this wait, the garbage collector's discovery-driven informers
	// can trip a conversion webhook with a List/Watch before that listener
	// is accepting connections, failing with "connection refused".
	gcWebhookReadyTimeout = 30 * time.Second

	// gcWebhookDialTimeout bounds a single dial attempt made while polling
	// for the webhook server's readiness.
	gcWebhookDialTimeout = 5 * time.Second

	// gcWebhookPollInterval is how often waitForWebhookServer retries the
	// dial while waiting for the webhook server to come up.
	gcWebhookPollInterval = 100 * time.Millisecond
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

	// webhook is the process-wide webhook config, wired in by buildManager
	// via setWebhookConfig (see webhookAware) after every Option has been
	// applied. nil means WithWebhookServer was never used for this New()
	// call, so the runner has nothing to wait for.
	webhook *WebhookConfig
}

// webhookAware is implemented by Controllers that need to know, before
// their own SetupWithManager runs, whether WithWebhookServer was used for
// this New() call and where its listener binds. This can't be captured
// inside WithGarbageCollector's own Option closure: Options run in the
// caller-supplied order, so a WithWebhookServer call later in the same
// opts list wouldn't have populated cfg.webhook yet at that point.
// buildManager wires it in instead, once cfg is fully resolved.
type webhookAware interface {
	setWebhookConfig(cfg *WebhookConfig)
}

// Compile-time assertion that garbageCollectorController implements webhookAware.
var _ webhookAware = (*garbageCollectorController)(nil)

func (g *garbageCollectorController) SetupWithManager(mgr Manager) error {
	runner := &garbageCollectorRunner{
		restConfig: mgr.GetConfig(),
		cfg:        g.cfg,
	}

	if g.webhook != nil {
		runner.webhookAddr = resolvedWebhookAddr(g.webhook)
	}

	err := mgr.Add(runner)
	if err != nil {
		return fmt.Errorf("failed to register garbage collector with manager: %w", err)
	}

	return nil
}

func (g *garbageCollectorController) setWebhookConfig(cfg *WebhookConfig) {
	g.webhook = cfg
}

// resolvedWebhookAddr returns the host:port a webhook server built from cfg
// binds to, matching the default controller-runtime itself applies
// (webhook.DefaultPort) when cfg.Port is left at zero. Host is always
// webhookHost (see manager.go): the manager's webhook server only ever
// binds to loopback.
func resolvedWebhookAddr(cfg *WebhookConfig) string {
	port := cfg.Port
	if port <= 0 {
		port = webhook.DefaultPort
	}

	return net.JoinHostPort(webhookHost, strconv.Itoa(port))
}

// garbageCollectorRunner orchestrates the garbage collector lifecycle as a
// controller-runtime manager.Runnable.
type garbageCollectorRunner struct {
	restConfig *restclient.Config
	cfg        GarbageCollectorConfig

	// webhookAddr is the manager's webhook server's host:port, set only
	// when WithWebhookServer was used for this process (see
	// garbageCollectorController.SetupWithManager). Empty means Start has
	// nothing to wait for.
	webhookAddr string
}

// Compile-time assertion that the runner implements manager.Runnable.
var _ manager.Runnable = (*garbageCollectorRunner)(nil)

// Start waits for the manager's webhook server to become reachable (if one
// was configured via WithWebhookServer - see gcWebhookReadyTimeout's doc for
// why), then builds the typed, metadata, and discovery clients, the REST
// mapper, the shared and metadata informer factories, and the
// GarbageCollector itself - using ctx, the Manager's own running context,
// rather than one captured when the Controller was set up - then runs the
// collector's workers, its discovery resync loop, and the REST mapper
// refresh loop until ctx is cancelled. It blocks until all background
// goroutines have exited.
func (r *garbageCollectorRunner) Start(ctx context.Context) error {
	if r.webhookAddr != "" {
		err := waitForWebhookServer(ctx, r.webhookAddr, gcWebhookReadyTimeout)
		if err != nil {
			return err
		}
	}

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

// waitForWebhookServer blocks until a TLS handshake against addr succeeds,
// polling every gcWebhookPollInterval, or returns ErrGarbageCollectorWebhookNotReady
// once timeout elapses or ctx is cancelled, whichever comes first. It mirrors
// webhook.DefaultServer's own StartedChecker dial
// (sigs.k8s.io/controller-runtime/pkg/webhook, server.go): InsecureSkipVerify
// is safe here because the goal is only to confirm the listener is accepting
// TLS connections at all, never to validate or use the self-signed
// certificate's identity.
func waitForWebhookServer(ctx context.Context, addr string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: gcWebhookDialTimeout},
		//nolint:gosec // dial-only readiness probe against our own loopback webhook server; no data is exchanged or trusted.
		Config: &tls.Config{InsecureSkipVerify: true},
	}

	ticker := time.NewTicker(gcWebhookPollInterval)
	defer ticker.Stop()

	var lastErr error

	for {
		dialCtx, dialCancel := context.WithTimeout(waitCtx, gcWebhookDialTimeout)
		conn, err := dialer.DialContext(dialCtx, "tcp", addr)

		dialCancel()

		if err == nil {
			closeErr := conn.Close()
			if closeErr != nil {
				return fmt.Errorf("failed to close webhook readiness probe connection: %w", closeErr)
			}

			return nil
		}

		lastErr = err

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("%w: timed out after %s dialing %s: %w",
				ErrGarbageCollectorWebhookNotReady, timeout, addr, lastErr)
		case <-ticker.C:
		}
	}
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
