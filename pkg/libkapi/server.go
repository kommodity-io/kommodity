package libkapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"

	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"
)

// readHeaderTimeout bounds how long the HTTP server waits for request
// headers, guarding against Slowloris-style connection exhaustion.
const readHeaderTimeout = 10 * time.Second

// Server is a built, not-yet-started libkapi server. Construct one with New.
type Server struct {
	mu sync.Mutex
	// cancelRun is nil until ListenAndServe starts the server, and doubles as
	// the "have we started" flag - it is only ever set alongside starting the
	// run loop, so a separate started bool would only ever agree with it.
	cancelRun context.CancelFunc

	// runWg tracks the apiserver run-loop goroutine started in
	// ListenAndServe, so Shutdown can wait for it to actually exit.
	runWg sync.WaitGroup

	addr         string
	httpServer   *http.Server
	aggregator   *aggregatorapiserver.APIAggregator
	storageClose func()
	logger       *slog.Logger
}

// New builds a full generic apiserver + apiextensions (CRD) server +
// aggregation layer, wired to the standard Kubernetes API groups (core v1,
// apps/v1, batch/v1, rbac.authorization.k8s.io/v1, networking.k8s.io,
// storage.k8s.io) and backed by cfg.Storage, plus any caller-supplied HTTP
// handlers mounted alongside it. The server is not started until
// ListenAndServe is called.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.TLS != nil {
		return nil, fmt.Errorf("Config.TLS: %w", ErrNotImplemented)
	}

	addr := cfg.resolvedAddr()
	logger := cfg.resolvedLogger()

	// Bridge klog to the consumer's slog logger before any k8s package
	// starts logging. The Kubernetes packages libkapi embeds use klog
	// internally; without this, their output goes to klog's default stderr
	// writer instead of the consumer's logger.
	InstallKlogAdapter(logger)

	handle, err := storage.Resolve(ctx, cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve storage backend: %w", err)
	}

	// buildServer's own chain eventually registers a PostStartHookFunc
	// (bootstrapDefaultNamespaceHook) that takes its own
	// genericapiserver.PostStartHookContext, supplied later by the apiserver
	// runtime when the hook actually runs - it is not, and should not be,
	// derived from this constructor's ctx.
	//nolint:contextcheck
	server, err := buildServer(cfg, addr, handle, logger)
	if err != nil {
		handle.Close()

		return nil, err
	}

	logger.Info("libkapi server built", "addr", addr)

	return server, nil
}

func buildServer(cfg Config, addr string, handle *storage.Handle, logger *slog.Logger) (*Server, error) {
	scheme, codecs, err := newScheme(cfg.Scheme)
	if err != nil {
		return nil, err
	}

	// Resolved once here and threaded through both setupAPIServerConfig (the
	// server's actual Authorization.Authorizer) and standardAPIGroups (rbac's
	// bootstrap-roles wiring), so both agree on the exact same instance
	// instead of each independently constructing their own default.
	authz := defaultAuthorizer()

	groups := standardAPIGroups(authz)

	err = resolveStandardGroupVersions(scheme, groups)
	if err != nil {
		return nil, err
	}

	allGroupVersions := append(groupVersions(groups),
		apiextensionsv1.SchemeGroupVersion, apiregistrationv1.SchemeGroupVersion)

	genericServerConfig, err := setupAPIServerConfig(
		addr, scheme, codecs, allGroupVersions, defaultAuthenticator(), authz)
	if err != nil {
		return nil, err
	}

	aggregator, mux, err := buildDelegationChain(cfg, genericServerConfig, codecs, groups, handle.Endpoints())
	if err != nil {
		return nil, err
	}

	return &Server{
		addr: addr,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
		},
		aggregator:   aggregator,
		storageClose: handle.Close,
		logger:       logger,
	}, nil
}

// buildDelegationChain builds the CRD server -> standard-API delegate ->
// aggregator delegation chain and returns the aggregator plus the
// caller-facing mux (custom handlers layered over the aggregator's Handler).
func buildDelegationChain(
	cfg Config,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	groups []standardAPIGroup,
	storageEndpoints []string,
) (*aggregatorapiserver.APIAggregator, *http.ServeMux, error) {
	crdServer, err := newAPIExtensionServer(
		genericServerConfig, codecs, storageEndpoints, genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, nil, err
	}

	genericServer, err := genericServerConfig.Complete().New("libkapi", crdServer.GenericAPIServer)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build the generic api server: %w", err)
	}

	err = installStandardAPIGroups(
		genericServer, groups, codecs, genericServerConfig.MergedResourceConfig, storageEndpoints)
	if err != nil {
		return nil, nil, err
	}

	aggregatorServer, err := newAPIAggregatorServer(genericServerConfig, codecs, storageEndpoints, genericServer,
		crdServer.Informers.Apiextensions().V1().CustomResourceDefinitions())
	if err != nil {
		return nil, nil, err
	}

	_, err = aggregatorServer.PrepareRun()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare aggregator server: %w", err)
	}

	mux, err := buildMux(cfg.Handlers, aggregatorServer.GenericAPIServer.Handler)
	if err != nil {
		return nil, nil, err
	}

	return aggregatorServer, mux, nil
}

// ListenAndServe binds the listener and blocks until ctx is canceled,
// Shutdown is called, or an unrecoverable error occurs. Canceling ctx
// gracefully shuts down both the HTTP server and the apiserver run loop.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.mu.Lock()
	if s.cancelRun != nil {
		s.mu.Unlock()

		return ErrServerAlreadyStarted
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancelRun = cancel
	s.mu.Unlock()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", s.addr)
	if err != nil {
		s.mu.Lock()
		s.cancelRun = nil
		s.mu.Unlock()

		cancel()

		return fmt.Errorf("failed to listen on %q: %w", s.addr, err)
	}

	// NonBlockingRunWithContext starts the apiserver's post-start hooks
	// (controllers, informers, auto-registration) and returns immediately.
	// We use this instead of RunWithContext because RunWithContext blocks
	// forever on nil stoppedCh/listenerStoppedCh channels when
	// SecureServingInfo is nil (our design), which would prevent Shutdown
	// from ever completing. The context passed here controls when the
	// post-start hooks' goroutines are stopped.
	// PrepareRun is called again on the underlying GenericAPIServer; this is
	// safe because it is idempotent (route installation overwrites existing
	// handlers, and lifecycle signal setup is a no-op on repeat).
	prepared := s.aggregator.GenericAPIServer.PrepareRun()

	_, _, err = prepared.NonBlockingRunWithContext(runCtx, readHeaderTimeout)
	if err != nil {
		s.mu.Lock()
		s.cancelRun = nil
		s.mu.Unlock()

		cancel()

		return fmt.Errorf("failed to start apiserver: %w", err)
	}

	// The run loop goroutine waits for the context to be canceled, so
	// Shutdown can track it with runWg and know the apiserver's background
	// work has been signaled to stop before returning.
	s.runWg.Go(func() {
		<-runCtx.Done()
	})

	// If the caller's ctx is canceled (rather than Shutdown being called
	// explicitly), gracefully shut down the HTTP server so Serve unblocks
	// instead of serving indefinitely. The shutdown context is derived
	// from context.Background (via WithoutCancel) because runCtx is
	// already canceled at that point.
	go func() {
		<-runCtx.Done()

		shutdownCtx, shutdownCancel := context.WithCancel(context.WithoutCancel(runCtx))
		defer shutdownCancel()

		_ = s.httpServer.Shutdown(shutdownCtx)
	}()

	err = s.httpServer.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server exited: %w", err)
	}

	return nil
}

// Shutdown gracefully stops the HTTP listener, the apiserver's background run
// loop, and (if cfg.Storage spawned one) the embedded Kine endpoint, waiting
// for each to actually finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancelRun
	s.cancelRun = nil
	s.mu.Unlock()

	if cancel == nil {
		return ErrServerNotStarted
	}

	cancel()

	err := s.httpServer.Shutdown(ctx)

	s.runWg.Wait()

	preShutdownErr := s.aggregator.GenericAPIServer.RunPreShutdownHooks()
	if preShutdownErr != nil {
		s.logger.Error("pre-shutdown hooks failed", "error", preShutdownErr)
	}

	s.aggregator.GenericAPIServer.Destroy()

	s.storageClose()

	if err != nil {
		return fmt.Errorf("failed to shut down http server: %w", err)
	}

	return nil
}
