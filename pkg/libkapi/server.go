package libkapi

import (
	"context"
	"crypto/rsa"
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

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
	"github.com/kommodity-io/kommodity/pkg/libkapi/controllers"
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

// newMu serializes New() across concurrent calls. Constructing a Server
// mutates several process-wide globals in the Kubernetes packages libkapi
// embeds - klog's contextual logger (InstallKlogAdapter) and the
// legacyscheme.Scheme singleton the upstream REST storage providers in
// registry.go are hard-wired to (see scheme.go's schemeMu doc) - none of
// which are safe for concurrent writers. Two Servers built at once without
// this lock race on that shared state, up to and including a fatal
// "concurrent map writes" crash. The cost is that one Server's construction
// can't overlap another's; ListenAndServe and everything after New returns
// is unaffected.
//nolint:gochecknoglobals // guards several k8s.io package-level singletons New touches.
var newMu sync.Mutex

// New builds a full generic apiserver + apiextensions (CRD) server +
// aggregation layer, wired to the standard Kubernetes API groups (core v1,
// apps/v1, batch/v1, rbac.authorization.k8s.io/v1, networking.k8s.io,
// storage.k8s.io) and backed by cfg.Storage, plus any caller-supplied HTTP
// handlers mounted alongside it. The server is not started until
// ListenAndServe is called.
//
// Auth options configure authentication (OIDC, ServiceAccount) and
// authorization (custom or admin authorizer). If no options are passed, the
// server defaults to anonymous authentication and always-allow authorization.
func New(ctx context.Context, cfg Config, opts ...auth.Option) (*Server, error) {
	newMu.Lock()
	defer newMu.Unlock()

	if cfg.TLS != nil {
		return nil, fmt.Errorf("Config.TLS: %w", ErrNotImplemented)
	}

	addr := cfg.resolvedAddr()
	logger := cfg.resolvedLogger()

	// Bridge klog, gRPC, and logrus loggers to the consumer's slog
	// logger before any k8s, gRPC, or kine package starts logging. The
	// Kubernetes packages libkapi embeds use klog internally; gRPC (used
	// by the etcd3 client and kine) uses grpclog; kine uses logrus.
	// Without these bridges, their output goes to their default stderr
	// writers instead of the consumer's logger. Must happen before
	// auth.Resolve, storage.Resolve, and buildServer so that resolution
	// and construction logs are also captured.
	//
	// The gRPC adapter demotes INFO/WARNING to slog.Debug so connection
	// lifecycle messages don't clutter normal output. The logrus
	// adapter additionally neutralizes logrus.Fatalf (called by kine's
	// compaction transaction on DB errors) so a momentary DB outage
	// logs and recovers instead of killing the process via os.Exit(1).
	InstallKlogAdapter(logger)
	InstallGRPCLogAdapter(logger)
	InstallLogrusAdapter(logger)

	// Create subloggers with a component field so log output is attributable
	// to the subsystem that produced it.
	serverLogger := logger.With("component", "server")
	authLogger := logger.With("component", "auth")
	controllersLogger := logger.With("component", "controllers")

	authCfg, err := auth.Resolve(ctx, opts, authLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to configure authentication: %w", err)
	}

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
	server, err := buildServer(cfg, addr, handle, authCfg, controllersLogger, serverLogger)
	if err != nil {
		handle.Close()

		return nil, err
	}

	serverLogger.Info("Built libkapi server", "addr", addr)

	return server, nil
}

func buildServer(cfg Config, addr string, handle *storage.Handle,
	authCfg *auth.ResolvedConfig, controllersLogger *slog.Logger,
	logger *slog.Logger) (*Server, error) {
	scheme, codecs, err := newScheme(cfg.Scheme)
	if err != nil {
		return nil, err
	}

	// Authorizer is resolved early: standardAPIGroups needs it for RBAC's
	// bootstrap-roles wiring, and it must be the same instance the server uses.
	authz := authCfg.Authorizer

	groups := standardAPIGroups(authz)

	err = resolveStandardGroupVersions(scheme, groups)
	if err != nil {
		return nil, err
	}

	allGroupVersions := append(groupVersions(groups),
		apiextensionsv1.SchemeGroupVersion, apiregistrationv1.SchemeGroupVersion)

	// Pass nil for authn — the SA authenticator needs LoopbackClientConfig
	// (available after setupAPIServerConfig), so the final union authenticator
	// is assembled and set in resolveAndSetAuth.
	genericServerConfig, err := setupAPIServerConfig(
		addr, scheme, codecs, allGroupVersions, nil, authz)
	if err != nil {
		return nil, err
	}

	err = resolveAndSetAuth(authCfg, genericServerConfig, controllersLogger)
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

// resolveAndSetAuth builds the SA authenticator (if configured), assembles
// the final union authenticator, sets it on genericServerConfig, and
// registers the SA controller hooks (token controller, key persistence,
// signing key rotation).
//
// When the SA signing key is explicitly provided (or ephemeral, with no
// KeyPersistence), the SA authenticator is built here and the token
// controller hook is registered. When the key must be loaded from a
// persisted Secret (SigningKey nil + KeyPersistence set), a
// DynamicAuthenticator placeholder is installed and the combined
// ServiceAccountSetupHook resolves the key, builds the authenticator, and
// swaps it in after the server starts.
func resolveAndSetAuth(
	authCfg *auth.ResolvedConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	controllersLogger *slog.Logger,
) error {
	if authCfg.SAConfig == nil {
		genericServerConfig.Authentication.Authenticator = auth.BuildUnionAuthenticator(
			authCfg.OIDCAuthenticator, nil)

		if len(authCfg.APIAudiences) > 0 {
			genericServerConfig.Authentication.APIAudiences = authCfg.APIAudiences
		}

		return nil
	}

	if authCfg.SAConfig.SigningKey == nil && authCfg.SAConfig.KeyPersistence != nil {
		return resolveDeferredSAAuth(authCfg, genericServerConfig, controllersLogger)
	}

	return resolveImmediateSAAuth(authCfg, genericServerConfig, controllersLogger)
}

// resolveDeferredSAAuth handles the case where the signing key must be
// loaded from a persisted Secret after the server starts. A
// DynamicAuthenticator placeholder is installed with OIDC + anonymous, and
// the combined ServiceAccountSetupHook resolves the key, builds the SA
// authenticator, swaps it in, and starts the token controller + rotation.
func resolveDeferredSAAuth(
	authCfg *auth.ResolvedConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	controllersLogger *slog.Logger,
) error {
	placeholder := auth.BuildUnionAuthenticator(authCfg.OIDCAuthenticator, nil)
	dynAuth := auth.NewDynamicAuthenticator(placeholder)

	genericServerConfig.Authentication.Authenticator = dynAuth

	if len(authCfg.APIAudiences) > 0 {
		genericServerConfig.Authentication.APIAudiences = authCfg.APIAudiences
	}

	setupHook, err := controllers.NewServiceAccountSetupHook(
		controllers.ServiceAccountSetupHookConfig{
			SACfg:           authCfg.SAConfig,
			OIDCAuth:        authCfg.OIDCAuthenticator,
			SetAuthenticator: dynAuth.Set,
			LoopbackConfig:  genericServerConfig.LoopbackClientConfig,
			InformerFactory: genericServerConfig.SharedInformerFactory,
			Logger:          controllersLogger,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to build service account setup hook: %w", err)
	}

	err = genericServerConfig.AddPostStartHook("libkapi-service-account-setup", setupHook)
	if err != nil {
		return fmt.Errorf("failed to add service account setup post-start hook: %w", err)
	}

	return nil
}

// resolveImmediateSAAuth handles the case where the signing key is available
// during New (either provided by the caller or generated as ephemeral). The
// SA authenticator is built here and the token controller hook is registered.
// If KeyPersistence is set, a create-only persistence hook and the signing
// key rotation hook are also registered.
func resolveImmediateSAAuth(
	authCfg *auth.ResolvedConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	controllersLogger *slog.Logger,
) error {
	signingKey, err := auth.ResolveSigningKey(authCfg.SAConfig)
	if err != nil {
		return fmt.Errorf("failed to resolve service account signing key: %w", err)
	}

	saAuthn, err := auth.BuildSAAuthenticator(authCfg.SAConfig, signingKey,
		genericServerConfig.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to build service account authenticator: %w", err)
	}

	genericServerConfig.Authentication.Authenticator = auth.BuildUnionAuthenticator(
		authCfg.OIDCAuthenticator, saAuthn)

	if len(authCfg.APIAudiences) > 0 {
		genericServerConfig.Authentication.APIAudiences = authCfg.APIAudiences
	}

	return registerSAControllerHooks(authCfg.SAConfig, signingKey, genericServerConfig, controllersLogger)
}

// registerSAControllerHooks builds and registers the SA token controller,
// key persistence, and signing-key rotation hooks on the genericServerConfig.
// All are started as post-start hooks by the apiserver runtime once the
// server is listening.
func registerSAControllerHooks(
	saCfg *auth.ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	genericServerConfig *genericapiserver.RecommendedConfig,
	logger *slog.Logger,
) error {
	err := registerTokenControllerHook(saCfg, signingKey, genericServerConfig)
	if err != nil {
		return err
	}

	if saCfg.KeyPersistence == nil {
		return nil
	}

	return registerKeyHooks(saCfg, signingKey, genericServerConfig, logger)
}

// registerTokenControllerHook builds and registers the SA token controller hook.
func registerTokenControllerHook(
	saCfg *auth.ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	genericServerConfig *genericapiserver.RecommendedConfig,
) error {
	issuer := saCfg.Issuer
	if issuer == "" {
		issuer = "kubernetes/serviceaccount"
	}

	tokenHook, err := controllers.NewTokenControllerHook(
		controllers.TokenControllerHookConfig{
			Issuer:     issuer,
			SigningKey: signingKey,
			RootCA:     saCfg.RootCA,
		},
		genericServerConfig.LoopbackClientConfig,
		genericServerConfig.SharedInformerFactory,
	)
	if err != nil {
		return fmt.Errorf("failed to build token controller hook: %w", err)
	}

	err = genericServerConfig.AddPostStartHook("libkapi-service-account-token-controller", tokenHook)
	if err != nil {
		return fmt.Errorf("failed to add token controller post-start hook: %w", err)
	}

	return nil
}

// registerKeyHooks builds and registers the key persistence and signing-key
// rotation hooks. Only called when saCfg.KeyPersistence is non-nil and the
// signing key was available during New (provided or ephemeral).
//
// The persistence hook creates the Secret if it doesn't exist (create-only,
// does not overwrite an existing Secret). The rotation hook watches the
// Secret for changes and rotates SA tokens when the key changes.
func registerKeyHooks(
	saCfg *auth.ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	genericServerConfig *genericapiserver.RecommendedConfig,
	logger *slog.Logger,
) error {
	persistenceHook, err := controllers.NewCreateKeyHook(
		controllers.CreateKeyHookConfig{
			SigningKey: signingKey,
			Namespace:  auth.ResolveSigningKeyNamespace(saCfg.KeyPersistence),
			SecretName: auth.ResolveSigningKeySecretName(saCfg.KeyPersistence),
		},
		genericServerConfig.LoopbackClientConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to build key persistence hook: %w", err)
	}

	err = genericServerConfig.AddPostStartHook("libkapi-signing-key-persistence", persistenceHook)
	if err != nil {
		return fmt.Errorf("failed to add key persistence post-start hook: %w", err)
	}

	rotationHook, err := controllers.NewSigningKeyRotationHook(
		controllers.SigningKeyRotationHookConfig{
			KeyPersistence: saCfg.KeyPersistence,
			SigningKey:     signingKey,
		},
		genericServerConfig.LoopbackClientConfig,
		genericServerConfig.SharedInformerFactory,
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to build signing key rotation hook: %w", err)
	}

	err = genericServerConfig.AddPostStartHook("libkapi-signing-key-rotation", rotationHook)
	if err != nil {
		return fmt.Errorf("failed to add signing key rotation post-start hook: %w", err)
	}

	return nil
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

	// PrepareRun is not called here: it installs /healthz, /livez, /readyz
	// and OpenAPI routes on the PathRecorderMux, and calling it twice (once
	// here, once in ListenAndServe) produces "duplicate path registration"
	// errors. The Handler is already populated by NewWithDelegate, so
	// buildMux can mount it before PrepareRun runs. ListenAndServe calls
	// PrepareRun exactly once before NonBlockingRunWithContext.
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
	// PrepareRun installs /healthz, /livez, /readyz and OpenAPI routes on
	// the PathRecorderMux; it must be called exactly once (calling it
	// twice produces "duplicate path registration" errors).
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
		s.logger.Error("Pre-shutdown hooks failed", "error", preShutdownErr)
	}

	s.aggregator.GenericAPIServer.Destroy()

	s.storageClose()

	if err != nil {
		return fmt.Errorf("failed to shut down http server: %w", err)
	}

	return nil
}
