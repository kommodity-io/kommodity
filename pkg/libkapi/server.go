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
)

// readHeaderTimeout bounds how long the HTTP server waits for request
// headers, guarding against Slowloris-style connection exhaustion.
const readHeaderTimeout = 10 * time.Second

// runner is satisfied by the prepared aggregator server New returns from
// PrepareRun(); it is declared here, rather than named directly, because
// k8s.io/kube-aggregator's own type for it is unexported.
type runner interface {
	Run(ctx context.Context) error
}

// Server is a built, not-yet-started libkapi server. Construct one with New.
type Server struct {
	mu sync.Mutex
	// cancelRun is nil until ListenAndServe starts the server, and doubles as
	// the "have we started" flag - it is only ever set alongside starting the
	// run loop, so a separate started bool would only ever agree with it.
	cancelRun context.CancelFunc

	addr         string
	httpServer   *http.Server
	prepared     runner
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

	storage, err := resolveStorage(ctx, cfg.Storage)
	if err != nil {
		return nil, err
	}

	// buildServer's own chain eventually registers a PostStartHookFunc
	// (bootstrapDefaultNamespaceHook) that takes its own
	// genericapiserver.PostStartHookContext, supplied later by the apiserver
	// runtime when the hook actually runs - it is not, and should not be,
	// derived from this constructor's ctx.
	//nolint:contextcheck
	server, err := buildServer(cfg, addr, storage, logger)
	if err != nil {
		storage.close()

		return nil, err
	}

	logger.Info("libkapi server built", "addr", addr)

	return server, nil
}

func buildServer(cfg Config, addr string, storage *storageHandle, logger *slog.Logger) (*Server, error) {
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

	prepared, mux, err := buildDelegationChain(cfg, genericServerConfig, codecs, groups, storage.endpoints)
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
		prepared:     prepared,
		storageClose: storage.close,
		logger:       logger,
	}, nil
}

// buildDelegationChain builds the CRD server -> standard-API delegate ->
// aggregator delegation chain and returns its prepared run loop plus the
// caller-facing mux (custom handlers layered over the aggregator's Handler).
func buildDelegationChain(
	cfg Config,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	groups []standardAPIGroup,
	storageEndpoints []string,
) (runner, *http.ServeMux, error) {
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

	prepared, err := aggregatorServer.PrepareRun()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare aggregator server: %w", err)
	}

	mux, err := buildMux(cfg.Handlers, aggregatorServer.GenericAPIServer.Handler)
	if err != nil {
		return nil, nil, err
	}

	return prepared, mux, nil
}

// ListenAndServe binds the listener and blocks until ctx is canceled,
// Shutdown is called, or an unrecoverable error occurs.
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
		cancel()

		return fmt.Errorf("failed to listen on %q: %w", s.addr, err)
	}

	go func() {
		err := s.prepared.Run(runCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("apiserver run loop exited", "error", err)
		}
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
	s.mu.Unlock()

	if cancel == nil {
		return ErrServerNotStarted
	}

	cancel()

	err := s.httpServer.Shutdown(ctx)

	s.storageClose()

	if err != nil {
		return fmt.Errorf("failed to shut down http server: %w", err)
	}

	return nil
}
