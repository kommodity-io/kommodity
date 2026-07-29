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

	"google.golang.org/grpc"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	restclient "k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kommodity-io/kommodity/pkg/libkapi/apiserver"
	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
	"github.com/kommodity-io/kommodity/pkg/libkapi/controllers"
	"github.com/kommodity-io/kommodity/pkg/libkapi/logging"
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
	grpcServer   *grpc.Server
	storageClose func()
	logger       *slog.Logger

	// mgr is nil unless at least one Controller was registered via
	// WithController. mgrCancel and mgrDone are set by ListenAndServe, only
	// when mgr is non-nil - see the Manager lifecycle doc on ListenAndServe.
	mgr       ctrl.Manager
	mgrCancel context.CancelFunc
	mgrDone   chan struct{}

	// internalHooksCancel cancels the context ListenAndServe passes to
	// NonBlockingRunWithContext, which drives the apiserver's OWN internal
	// post-start hooks (CRD establishing/naming/nonstructural-schema
	// controllers, autoregister, informer sync) - not the
	// WithPostStartHook/WithPreShutdownHook registrations below. Set by
	// ListenAndServe; deliberately never canceled anywhere but Shutdown -
	// see the doc on ListenAndServe's NonBlockingRunWithContext call.
	internalHooksCancel context.CancelFunc

	// loopbackConfig is the server's own privileged (system:masters-equivalent)
	// identity, handed to each PostStartHookFunc/PreShutdownHookFunc - the
	// same config Controller/Manager use internally (see buildManager).
	loopbackConfig *restclient.Config

	// postStartHooks and preShutdownHooks are the WithPostStartHook/
	// WithPreShutdownHook registrations, run by ListenAndServe and Shutdown
	// respectively - see their own docs.
	postStartHooks   []PostStartHookFunc
	preShutdownHooks []PreShutdownHookFunc
}

// newMu serializes New() across concurrent calls. Constructing a Server
// mutates several process-wide globals in the Kubernetes packages libkapi
// embeds - klog's contextual logger (logging.InstallKlogAdapter) and the
// legacyscheme.Scheme singleton the upstream REST storage providers in
// apiserver/registry.go are hard-wired to (see apiserver/scheme.go's
// schemeMu doc) - none of
// which are safe for concurrent writers. Two Servers built at once without
// this lock race on that shared state, up to and including a fatal
// "concurrent map writes" crash. The cost is that one Server's construction
// can't overlap another's; ListenAndServe and everything after New returns
// is unaffected.
//
//nolint:gochecknoglobals // guards several k8s.io package-level singletons New touches.
var newMu sync.Mutex

// New builds a full generic apiserver + apiextensions (CRD) server +
// aggregation layer, wired to the standard Kubernetes API groups (core v1,
// apps/v1, batch/v1, rbac.authorization.k8s.io/v1, networking.k8s.io,
// storage.k8s.io) and backed by the storage configured via WithStorage, plus
// any caller-supplied HTTP handlers mounted alongside it. The server is not
// started until ListenAndServe is called.
//
// Options configure everything: listener address, storage, logging, extra
// HTTP handlers, scheme, plus authentication (OIDC, ServiceAccount) and
// authorization (custom or admin authorizer). If no auth options are passed,
// the server defaults to anonymous authentication and always-allow
// authorization.
func New(ctx context.Context, opts ...Option) (*Server, error) {
	newMu.Lock()
	defer newMu.Unlock()

	cfg := config{}

	for _, opt := range opts {
		err := opt(ctx, &cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if cfg.tls != nil {
		return nil, fmt.Errorf("WithTLS: %w", ErrNotImplemented)
	}

	addr := cfg.resolvedAddr()
	logger := cfg.resolvedLogger()

	// Bridge klog, gRPC, logrus, and controller-runtime loggers to the
	// consumer's slog logger before any k8s, gRPC, kine, or
	// controller-runtime package starts logging. The Kubernetes packages
	// libkapi embeds use klog internally; gRPC (used by the etcd3 client
	// and kine) uses grpclog; kine uses logrus; sigs.k8s.io/controller-runtime
	// (used by WithController's Manager, and by any consumer calling it
	// directly, e.g. a plain client.New(...)) uses its own global logr
	// sink. Without these bridges, their output goes to their default
	// stderr writers - or, for controller-runtime specifically, is
	// silently discarded with a one-time "log.SetLogger(...) was never
	// called" warning - instead of the consumer's logger. Must happen
	// before auth.Resolve, storage.Resolve, and buildServer so that
	// resolution and construction logs are also captured.
	//
	// The gRPC adapter demotes INFO/WARNING to slog.Debug so connection
	// lifecycle messages don't clutter normal output. The logrus
	// adapter additionally neutralizes logrus.Fatalf (called by kine's
	// compaction transaction on DB errors) so a momentary DB outage
	// logs and recovers instead of killing the process via os.Exit(1).
	logging.InstallKlogAdapter(logger)
	logging.InstallGRPCLogAdapter(logger)
	logging.InstallLogrusAdapter(logger)
	logging.InstallControllerRuntimeLogAdapter(logger)

	// Create subloggers with a component field so log output is attributable
	// to the subsystem that produced it.
	serverLogger := logger.With("component", "server")
	authLogger := logger.With("component", "auth")
	controllersLogger := logger.With("component", "controllers")
	managerLogger := logger.With("component", "controller-manager")

	authCfg, err := auth.Resolve(ctx, cfg.authOpts, authLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to configure authentication: %w", err)
	}

	handle, err := storage.Resolve(ctx, cfg.storage)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve storage backend: %w", err)
	}

	// buildServer's own chain eventually registers a PostStartHookFunc
	// (bootstrapDefaultNamespaceHook) that takes its own
	// genericapiserver.PostStartHookContext, supplied later by the apiserver
	// runtime when the hook actually runs - it is not, and should not be,
	// derived from this constructor's ctx.
	//nolint:contextcheck
	server, err := buildServer(cfg, addr, handle, authCfg, controllersLogger, managerLogger, serverLogger)
	if err != nil {
		handle.Close()

		return nil, err
	}

	serverLogger.Info("Built libkapi server", "addr", addr)

	return server, nil
}

func buildServer(cfg config, addr string, handle *storage.Handle,
	authCfg *auth.ResolvedConfig, controllersLogger *slog.Logger,
	managerLogger *slog.Logger, logger *slog.Logger) (*Server, error) {
	scheme, codecs, err := apiserver.NewScheme(cfg.scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to build scheme: %w", err)
	}

	// Authorizer is resolved early: StandardAPIGroups needs it for RBAC's
	// bootstrap-roles wiring, and it must be the same instance the server uses.
	authz := authCfg.Authorizer

	groups := apiserver.StandardAPIGroups(authz)

	err = apiserver.ResolveStandardGroupVersions(scheme, groups)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve standard API group versions: %w", err)
	}

	allGroupVersions := append(apiserver.GroupVersions(groups),
		apiextensionsv1.SchemeGroupVersion, apiregistrationv1.SchemeGroupVersion)

	// Pass nil for authn — the SA authenticator needs LoopbackClientConfig
	// (available after SetupAPIServerConfig), so the final union authenticator
	// is assembled and set in resolveAndSetAuth.
	genericServerConfig, err := apiserver.SetupAPIServerConfig(
		addr, scheme, codecs, allGroupVersions, nil, authz)
	if err != nil {
		return nil, fmt.Errorf("failed to set up API server config: %w", err)
	}

	err = resolveAndSetAuth(authCfg, genericServerConfig, controllersLogger)
	if err != nil {
		return nil, err
	}

	// buildManager uses the exact same scheme and loopback identity as the
	// REST layer above - see the Manager lifecycle doc on ListenAndServe.
	mgr, err := buildManager(cfg, genericServerConfig.LoopbackClientConfig, scheme, managerLogger)
	if err != nil {
		return nil, err
	}

	aggregator, grpcServer, handler, err := buildDelegationChain(
		cfg, genericServerConfig, codecs, groups, handle.Endpoints())
	if err != nil {
		return nil, err
	}

	return &Server{
		addr: addr,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
		},
		aggregator:       aggregator,
		grpcServer:       grpcServer,
		storageClose:     handle.Close,
		logger:           logger,
		mgr:              mgr,
		loopbackConfig:   genericServerConfig.LoopbackClientConfig,
		postStartHooks:   cfg.postStartHooks,
		preShutdownHooks: cfg.preShutdownHooks,
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
			SACfg:            authCfg.SAConfig,
			OIDCAuth:         authCfg.OIDCAuthenticator,
			SetAuthenticator: dynAuth.Set,
			LoopbackConfig:   genericServerConfig.LoopbackClientConfig,
			InformerFactory:  genericServerConfig.SharedInformerFactory,
			Logger:           controllersLogger,
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
// caller-facing gRPC server (nil unless WithGRPCServerFactory was used) and
// handler (custom HTTP handlers and, if any, gRPC routing layered over the
// aggregator's own Handler).
func buildDelegationChain(
	cfg config,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	groups []apiserver.StandardAPIGroup,
	storageEndpoints []string,
) (*aggregatorapiserver.APIAggregator, *grpc.Server, http.Handler, error) {
	crdServer, err := apiserver.NewAPIExtensionServer(
		genericServerConfig, codecs, storageEndpoints, genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build delegation chain: %w", err)
	}

	genericServer, err := genericServerConfig.Complete().New("libkapi", crdServer.GenericAPIServer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build the generic api server: %w", err)
	}

	err = apiserver.InstallStandardAPIGroups(
		genericServer, groups, codecs, genericServerConfig.MergedResourceConfig, storageEndpoints)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build delegation chain: %w", err)
	}

	aggregatorServer, err := apiserver.NewAPIAggregatorServer(
		genericServerConfig, codecs, storageEndpoints, genericServer,
		crdServer.Informers.Apiextensions().V1().CustomResourceDefinitions())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build delegation chain: %w", err)
	}

	// PrepareRun is not called here: it installs /healthz, /livez, /readyz
	// and OpenAPI routes on the PathRecorderMux, and calling it twice (once
	// here, once in ListenAndServe) produces "duplicate path registration"
	// errors. The Handler is already populated by NewWithDelegate, so
	// buildMux can mount it before PrepareRun runs. ListenAndServe calls
	// PrepareRun exactly once before NonBlockingRunWithContext.
	mux, err := buildMux(cfg.handlers, aggregatorServer.GenericAPIServer.Handler)
	if err != nil {
		return nil, nil, nil, err
	}

	grpcServer, handler, err := buildHandler(cfg.grpcFactories, mux)
	if err != nil {
		return nil, nil, nil, err
	}

	return aggregatorServer, grpcServer, handler, nil
}

// ListenAndServe binds the listener and blocks until ctx is canceled,
// Shutdown is called, or an unrecoverable error occurs. Canceling ctx
// gracefully shuts down both the HTTP server and the apiserver run loop.
// Once the listener is bound, the apiserver's own internal post-start hooks
// start, followed by any WithPostStartHook registrations, in order; a
// hook's error fails ListenAndServe. If any Controller was registered via
// WithController, this also starts the controller manager (reconcilers,
// Runnables, and - if configured - the webhook server and leader election).
//
//nolint:contextcheck // internalHooksCtx is deliberately not derived from ctx - see its own doc, below.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.mu.Lock()
	if s.cancelRun != nil {
		s.mu.Unlock()

		return ErrServerAlreadyStarted
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancelRun = cancel

	// internalHooksCtx is deliberately not derived from ctx - see its own
	// doc, below, for why.
	internalHooksCtx, internalHooksCancel := context.WithCancel(context.Background())
	s.internalHooksCancel = internalHooksCancel
	s.mu.Unlock()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return s.abortListenAndServe(cancel, internalHooksCancel, fmt.Errorf("failed to listen on %q: %w", s.addr, err))
	}

	// Registered before Serve even starts, so any of the early-return
	// failure paths below (which all cancel runCtx to unwind) reliably stop
	// the Serve goroutine too, instead of leaking it.
	s.registerShutdownWatcher(runCtx)

	// Serve must actually be running - not just listener bound - before
	// anything (a WithPostStartHook, a Controller's Runnable) can reach this
	// server over the network: the listener being bound only means the OS
	// will accept the TCP handshake into its backlog, nothing reads or
	// responds on it until Serve's Accept loop is running. So Serve starts
	// here, in its own goroutine, before post-start hooks, the apiserver's
	// own internal post-start hooks, and the manager - all of which may need
	// to call back into this same server - rather than being called
	// synchronously at the very end as it used to be.
	serveErr := make(chan error, 1)

	go func() {
		serveErr <- s.httpServer.Serve(listener)
	}()

	// NonBlockingRunWithContext starts the apiserver's own internal
	// post-start hooks (EstablishingController, NamingConditionController,
	// autoregister, informer sync, ...) and returns immediately - it just
	// spawns a goroutine per hook and returns, it does not wait for any of
	// them to finish. We use this instead of RunWithContext because
	// RunWithContext blocks forever on nil stoppedCh/listenerStoppedCh
	// channels when SecureServingInfo is nil (our design), which would
	// prevent Shutdown from ever completing. PrepareRun installs /healthz,
	// /livez, /readyz and OpenAPI routes on the PathRecorderMux; it must be
	// called exactly once (calling it twice produces "duplicate path
	// registration" errors).
	//
	// Deliberately placed before runPostStartHooks below, not after: a
	// WithPostStartHook that depends on one of these internal controllers
	// converging - e.g. creating a CRD and waiting for it to become
	// Established and resolvable via the RESTMapper before a
	// WithController reconciler tries to watch it - needs
	// EstablishingController etc. already running concurrently, or it
	// deadlocks forever waiting on a controller that was never started.
	//
	// internalHooksCtx is deliberately its own context, not runCtx: these
	// internal hooks treat their context being canceled before they finish
	// exactly like a timeout and call klog.Fatal (see PostStartHookFunc's
	// doc), so it must never be canceled while one might still be
	// in-flight - only Shutdown ever cancels it (see abortListenAndServe's
	// doc for why the failure paths below deliberately don't).
	prepared := s.aggregator.GenericAPIServer.PrepareRun()

	//nolint:contextcheck // internalHooksCtx is deliberately not derived from ctx - see its doc above.
	_, _, err = prepared.NonBlockingRunWithContext(internalHooksCtx, readHeaderTimeout)
	if err != nil {
		return s.abortListenAndServe(cancel, internalHooksCancel, fmt.Errorf("failed to start apiserver: %w", err))
	}

	// Run any WithPostStartHook registrations - in registration order,
	// synchronously - now that the internal hooks above are running and can
	// be depended on. A failing hook aborts ListenAndServe through
	// abortAfterInternalHooksStarted, not abortListenAndServe - see its doc
	// for why internalHooksCtx needs different handling once internal hooks
	// are actually running.
	err = s.runPostStartHooks(runCtx)
	if err != nil {
		//nolint:contextcheck // abortAfterInternalHooksStarted deliberately bounds its own wait - see its doc.
		return s.abortAfterInternalHooksStarted(cancel, internalHooksCancel, listener.Addr().String(),
			fmt.Errorf("post-start hook failed: %w", err))
	}

	// The run loop goroutine waits for the apiserver's internal hooks'
	// context to be canceled, so Shutdown can track it with runWg and know
	// that background work has been signaled to stop before returning.
	s.runWg.Go(func() {
		<-internalHooksCtx.Done()
	})

	// startManager's mgrCtx is intentionally not derived from ctx - see its doc.
	//nolint:contextcheck
	s.startManager()

	err = <-serveErr
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server exited: %w", err)
	}

	return nil
}

// Shutdown gracefully stops the controller manager (if any Controller was
// registered), runs any WithPreShutdownHook registrations, the HTTP
// listener, the apiserver's background run loop, and (if cfg.Storage
// spawned one) the embedded Kine endpoint, waiting for each to actually
// finish, in that order - the manager is stopped and the pre-shutdown hooks
// run, each given a real chance to finish its own cleanup, before the API
// server listener closes underneath them.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancelRun
	s.cancelRun = nil
	mgrCancel := s.mgrCancel
	mgrDone := s.mgrDone
	internalHooksCancel := s.internalHooksCancel
	s.mu.Unlock()

	if cancel == nil {
		return ErrServerNotStarted
	}

	// Stop the controller manager, and wait for it to fully finish, before
	// touching the API server or its listener - see the ordering rationale
	// on ListenAndServe. mgrCancel/mgrDone are still nil if ListenAndServe
	// hasn't reached startManager yet (e.g. it's still running a slow
	// WithPostStartHook) - nothing to stop in that case; cancel() below
	// still unblocks a well-behaved hook that's watching runCtx. Bounded by
	// ctx so Shutdown doesn't hang forever if the caller's ctx already
	// carries a short deadline.
	if mgrCancel != nil {
		mgrCancel()

		select {
		case <-mgrDone:
		case <-ctx.Done():
		}
	}

	// Run any WithPreShutdownHook registrations - in registration order,
	// while the listener is still open - before touching it. Bounded by ctx
	// the same way the manager's own stop is, above: a hook that ignores ctx
	// only delays Shutdown, it never hangs it forever.
	s.runPreShutdownHooks(ctx)

	cancel()
	internalHooksCancel()

	s.shutdownGRPCServer(ctx)

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

	// The shutdown watcher (registerShutdownWatcher) may have already shut down
	// the HTTP server with a fresh context before s.httpServer.Shutdown(ctx)
	// ran, causing it to return nil even though ctx has expired. Surface the
	// ctx error so callers see the deadline they set.
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return fmt.Errorf("failed to shut down http server: %w", ctxErr)
	}

	return nil
}

// shutdownGRPCServer gracefully stops s.grpcServer (if WithGRPCServerFactory
// was used), waiting for in-flight RPCs to finish. Bounded by ctx, the same
// way Shutdown bounds its wait for the controller manager: if ctx is done
// before GracefulStop returns, it falls back to Stop, which closes
// connections immediately rather than letting Shutdown hang forever on a
// client holding a stream open. A no-op when grpcServer is nil.
func (s *Server) shutdownGRPCServer(ctx context.Context) {
	if s.grpcServer == nil {
		return
	}

	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		s.grpcServer.GracefulStop()
	}()

	select {
	case <-stopped:
	case <-ctx.Done():
		s.grpcServer.Stop()
	}
}

// registerShutdownWatcher starts a goroutine that gracefully shuts down the
// HTTP server once runCtx is canceled - whether by the caller's own ctx
// being canceled, or by Shutdown - so Serve unblocks instead of serving
// indefinitely. The shutdown context is derived from context.Background
// (via WithoutCancel) because runCtx is already canceled at that point.
func (s *Server) registerShutdownWatcher(runCtx context.Context) {
	go func() {
		<-runCtx.Done()

		shutdownCtx, shutdownCancel := context.WithCancel(context.WithoutCancel(runCtx))
		defer shutdownCancel()

		_ = s.httpServer.Shutdown(shutdownCtx)
	}()
}

// internalHooksHealthyTimeout bounds how long
// abortAfterInternalHooksStarted waits for /healthz to confirm the
// apiserver's internal post-start hooks have all finished before canceling
// their context regardless. In practice these hooks (informer sync, a
// single namespace-create call, ...) finish within milliseconds; this only
// matters as a ceiling for a genuinely broken configuration.
const internalHooksHealthyTimeout = 5 * time.Second

// healthzPollInterval is how often waitForInternalHooksHealthy retries
// GET /healthz while waiting for it to report healthy.
const healthzPollInterval = 50 * time.Millisecond

// abortListenAndServe unwinds a startup attempt that failed before
// NonBlockingRunWithContext ever ran - a listener bind failure, or
// NonBlockingRunWithContext's own (in our configuration, unreachable, since
// SecureServingInfo is always nil - see its call site's doc) synchronous
// error. Nothing depends on internalHooksCtx yet in either case, so it's
// safe to cancel immediately alongside runCtx. Clears cancelRun so a later
// ListenAndServe/Shutdown call doesn't see a stale started state, then
// cancels both contexts - unblocking the shutdown-watcher goroutine already
// registered in ListenAndServe by this point, so the listener and Serve
// goroutine it started don't leak - and closes storage, since a failed
// ListenAndServe leaves a Server the caller won't call Shutdown on (it
// never started), before returning err.
func (s *Server) abortListenAndServe(
	cancel context.CancelFunc, internalHooksCancel context.CancelFunc, err error,
) error {
	s.mu.Lock()
	s.cancelRun = nil
	s.internalHooksCancel = nil
	s.mu.Unlock()

	cancel()
	internalHooksCancel()
	s.storageClose()

	return err
}

// abortAfterInternalHooksStarted unwinds a startup attempt that failed
// after NonBlockingRunWithContext already started the apiserver's internal
// post-start hooks - currently, a failing WithPostStartHook. Unlike
// abortListenAndServe, it can't cancel internalHooksCancel immediately:
// some of those internal hooks (e.g. apiextensions' "crd-informer-synced")
// treat their context being canceled before they finish exactly like a
// timeout and call klog.Fatal, crashing the whole process - not just this
// Server - which would defeat the entire point of WithPostStartHook
// returning an ordinary error instead of doing that itself. /healthz only
// reports healthy once every one of those hooks has already returned
// successfully (see k8s.io/apiserver/pkg/server/hooks.go's
// postStartHookHealthz), so it's the one generic signal available that
// canceling is now safe - this waits for it, bounded by
// internalHooksHealthyTimeout, before canceling, closing storage (see
// abortListenAndServe's doc for why), and returning err. addr is the bound
// listener's actual address (not s.addr, which may be a wildcard/ephemeral
// ":0" the OS resolved when binding).
func (s *Server) abortAfterInternalHooksStarted(
	cancel context.CancelFunc, internalHooksCancel context.CancelFunc, addr string, err error,
) error {
	healthyCtx, healthyCancel := context.WithTimeout(context.Background(), internalHooksHealthyTimeout)
	defer healthyCancel()

	waitForInternalHooksHealthy(healthyCtx, addr)

	s.mu.Lock()
	s.cancelRun = nil
	s.internalHooksCancel = nil
	s.mu.Unlock()

	internalHooksCancel()
	cancel()
	s.storageClose()

	return err
}

// waitForInternalHooksHealthy polls the local /healthz endpoint until it
// reports healthy or ctx is done - see abortAfterInternalHooksStarted's doc
// for why that's the signal being waited on.
func waitForInternalHooksHealthy(ctx context.Context, addr string) {
	client := http.Client{Timeout: time.Second}
	url := "http://" + addr + "/healthz"

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()

				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(healthzPollInterval):
		}
	}
}

// startManager starts the controller manager, if any Controller was
// registered via WithController, once the listener is bound. mgrCtx is
// deliberately derived from context.Background(), not ListenAndServe's own
// runCtx/ctx: Shutdown cancels it and waits for mgr.Start to return *before*
// tearing down the API server listener, so a Controller's own shutdown
// cleanup (e.g. deleting its own object, watching ctx.Done() and using a
// fresh context) has a real chance to land instead of racing a closed
// socket.
func (s *Server) startManager() {
	if s.mgr == nil {
		return
	}

	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	mgrDone := make(chan struct{})

	// mgrCancel/mgrDone are written under s.mu because Shutdown may read
	// them concurrently, from a different goroutine, at any time after
	// ListenAndServe starts - including before startManager has run.
	s.mu.Lock()
	s.mgrCancel = mgrCancel
	s.mgrDone = mgrDone
	s.mu.Unlock()

	go func() {
		defer close(mgrDone)

		mgrErr := s.mgr.Start(mgrCtx)
		if mgrErr != nil {
			s.logger.Error("Controller manager exited with an error", "error", mgrErr)
		}
	}()
}

// runPostStartHooks runs each WithPostStartHook registration, in
// registration order, synchronously - unlike k8s.io/apiserver's own
// PostStartHook mechanism, which runs all of its hooks concurrently, in
// unspecified order, and calls klog.Fatal on error (see PostStartHookFunc's
// doc). A failing hook here returns an ordinary error instead, so
// ListenAndServe can fail startup without crashing the process.
func (s *Server) runPostStartHooks(ctx context.Context) error {
	for i, hook := range s.postStartHooks {
		err := hook(ctx, s.loopbackConfig)
		if err != nil {
			return fmt.Errorf("post-start hook %d failed: %w", i, err)
		}
	}

	return nil
}

// runPreShutdownHooks runs each WithPreShutdownHook registration, in
// registration order, in its own goroutine, while the API server's listener
// is still open - giving each hook a real chance to make one last
// privileged API call. Bounded by ctx, the same way Shutdown bounds its wait
// for the controller manager: a hook that ignores ctx only delays Shutdown,
// it never hangs it forever. A hook's error is logged, not fatal - Shutdown
// must still finish tearing down the rest of the server regardless of any
// one cleanup step's failure.
func (s *Server) runPreShutdownHooks(ctx context.Context) {
	if len(s.preShutdownHooks) == 0 {
		return
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i, hook := range s.preShutdownHooks {
			err := hook(ctx, s.loopbackConfig)
			if err != nil {
				s.logger.Error("Pre-shutdown hook failed", "index", i, "error", err)
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}
