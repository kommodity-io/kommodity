# PRD: `WithController`

## Problem

A `libkapi.Server` gives a consuming application no way to run privileged
background work — a reconciler, a heartbeat loop, anything that needs its
own client against the server's API — against the server's own privileged
(system:masters-equivalent) identity. Internally, libkapi does exactly this
for its own concerns (the SA token controller, signing-key rotation) via
unexported `genericapiserver.PostStartHook`s in `pkg/libkapi/controllers`,
but that mechanism isn't exposed.

kontinuum needs this for its server registry: every kontinuum process
registers itself as a `kontinuum.io/v1alpha1` `Kontinuum` object, heartbeats
it, runs a TTL reconciler that expires stale ones, and deletes its own
object on graceful shutdown. All of that needs a privileged client and a
real chance to make one last API call as the process is shutting down —
today, nothing in the public API provides either.

## Proposed API

Three new `Option`s, following the shape `WithScheme`/`WithHTTPHandlerFactory`
already established in `pkg/libkapi/options.go`:

```go
// Controller registers reconcilers and/or runnables against the Manager
// libkapi builds, owns, and runs for the life of the Server, using the
// server's own privileged (system:masters-equivalent) loopback identity —
// the same one pkg/libkapi/controllers uses internally. SetupWithManager is
// called once per Controller, synchronously, during New — before the
// server starts serving. A returned error fails New. Implementations
// typically call ctrl.NewControllerManagedBy(mgr).For(&MyType{}).Complete(r)
// and/or mgr.Add(myRunnable) here. MyType's GVK must already be registered
// via WithScheme.
type Controller interface {
	SetupWithManager(mgr Manager) error
}

// Manager re-exports controller-runtime's Manager so callers implementing
// Controller don't need to import sigs.k8s.io/controller-runtime directly
// for this one type — matches the Authorizer re-export pattern in
// authoptions.go.
type Manager = ctrl.Manager

// WithController registers a Controller. Can be passed more than once;
// each is set up in the order given.
func WithController(c Controller) Option {
	return func(_ context.Context, cfg *config) error {
		cfg.controllers = append(cfg.controllers, c)

		return nil
	}
}

// LeaderElectionConfig configures manager-wide leader election, backed by a
// coordination.k8s.io/v1 Lease — the same default resource lock
// controller-runtime itself uses. When enabled, only the elected replica's
// Controllers run their reconcile loops and non-webhook Runnables; the rest
// block in mgr.Start until they win an election or the process exits.
// Webhook handlers are unaffected either way (see Lifecycle).
type LeaderElectionConfig struct {
	// ID names the Lease object contenders coordinate on. Required.
	ID string

	// Namespace is the Lease's namespace. Defaults to "default" — the one
	// namespace libkapi's own bootstrap-default-namespace hook guarantees
	// exists — if empty.
	Namespace string
}

// WithLeaderElection enables manager-wide leader election using the
// server's own privileged loopback identity to talk to its own Lease
// object. Without this option (today's only behavior), every registered
// Controller runs unmodified on every replica — they must be written to
// tolerate that.
func WithLeaderElection(cfg LeaderElectionConfig) Option {
	return func(_ context.Context, c *config) error {
		c.leaderElection = &cfg

		return nil
	}
}

// WebhookConfig configures the manager's webhook server. The server is
// bound to 127.0.0.1 only (see Lifecycle) — it exists to answer
// admission/conversion calls from the API server built into this same
// process, not to be reachable from anywhere else, so there's no Service or
// cluster networking to configure.
type WebhookConfig struct {
	// Port the webhook server listens on. Defaults to 9443
	// (controller-runtime's own default) if zero.
	Port int

	// DNSNames are the Subject Alternative Names New embeds in the
	// self-signed serving certificate it generates on startup (see
	// Lifecycle) if one doesn't already exist. Defaults to
	// []string{"localhost"} if empty — the only hostname a caller dialing
	// 127.0.0.1 from this same host would ever use.
	DNSNames []string
}

// WithWebhookServer enables the manager's webhook server. Any registered
// Controller can call mgr.GetWebhookServer().Register(path, handler) in its
// own SetupWithManager to serve admission or conversion webhooks — no
// change to the Controller interface. New ensures a self-signed serving
// certificate exists before the manager starts. Without this option,
// GetWebhookServer still works (controller-runtime's own lazy default), but
// with no certificate provisioned, so a Controller trying to actually serve
// traffic through it would fail.
func WithWebhookServer(cfg WebhookConfig) Option {
	return func(_ context.Context, c *config) error {
		if len(cfg.DNSNames) == 0 {
			cfg.DNSNames = []string{"localhost"}
		}

		c.webhook = &cfg

		return nil
	}
}
```

`config` gains three fields: `controllers []Controller`,
`leaderElection *LeaderElectionConfig`, `webhook *WebhookConfig`.

A new unexported helper, `pkg/libkapi/webhookcert.go` (mirroring `scheme.go`'s
single-responsibility style), provisions the webhook's serving certificate:

```go
// webhookCertDir is controller-runtime's own default webhook.Options.CertDir
// — reused as-is (not overridden) so the certificate New generates lands
// exactly where webhook.NewServer(webhook.Options{}) already looks by
// default.
//
// os.TempDir() is the fixed, stable OS temp directory (e.g. "/tmp") — the
// same path every process on the host resolves to. This must NOT be
// os.MkdirTemp(), which mints a fresh, uniquely-named directory on every
// call: that would make every restart generate (and immediately orphan) a
// new certificate, breaking exactly the across-restart reuse this option
// exists for, and defeating whatever caBundle the caller registered on a
// Validating/MutatingWebhookConfiguration.
var webhookCertDir = filepath.Join(os.TempDir(), "k8s-webhook-server", "serving-certs")

// ensureSelfSignedWebhookCert writes tls.crt/tls.key under webhookCertDir if
// they don't already exist there, using k8s.io/client-go/util/cert's
// GenerateSelfSignedCertKey — the same helper k8s.io/apiserver's own
// loopback-client cert generation uses (see apiserver.go's
// newLoopbackClientConfig doc) — so repeated New calls against the same
// /tmp reuse one certificate instead of invalidating whatever caBundle the
// caller registered on a Validating/MutatingWebhookConfiguration.
func ensureSelfSignedWebhookCert(dnsNames []string) error
```

## Lifecycle

libkapi builds and owns exactly **one** `ctrl.Manager` — it fans out to
however many `Controller`s are registered, not one manager per controller.

- **Build** (`buildServer`, only if `len(cfg.controllers) > 0`): the manager
  is built from `genericServerConfig.LoopbackClientConfig` and the *same*
  `*runtime.Scheme` value `newScheme(cfg.scheme)` already produced for the
  REST layer — not a fresh scheme, not `cfg.scheme` directly. This is the
  whole scheme-consistency story: one object, shared by reference, so
  whatever a caller registered via `WithScheme` before calling `New` is
  already visible to the manager's client. A `Controller` that needs a type
  the caller forgot to register fails loudly at `SetupWithManager` time
  (`ctrl.NewControllerManagedBy(...).For(&T{})` errors on an unknown GVK) —
  no separate validation needed. Base options:
  `ctrl.Options{Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"}`
  — libkapi already serves its own `/healthz`.
  - If `cfg.leaderElection != nil`: also set `LeaderElection: true`,
    `LeaderElectionResourceLock: "leases"`, `LeaderElectionID: cfg.leaderElection.ID`,
    `LeaderElectionNamespace: cfg.leaderElection.Namespace` (defaulted to
    `"default"` if empty), and `LeaderElectionConfig: genericServerConfig.LoopbackClientConfig`
    so election traffic uses the same privileged identity as everything
    else. Otherwise `LeaderElection` stays `false` (today's only behavior) —
    controllers must tolerate running unmodified on every replica.
  - If `cfg.webhook != nil`: call `ensureSelfSignedWebhookCert(cfg.webhook.DNSNames)`
    first (fails `New` on error, same as any other setup step), then set
    `WebhookServer: webhook.NewServer(webhook.Options{Host: "127.0.0.1", Port: cfg.webhook.Port})`.
    The webhook server is bound to loopback only — it exists to answer
    admission/conversion calls from the API server built into this same
    process, never a Service or any other host, so there's nothing to expose
    beyond `127.0.0.1`. Otherwise `WebhookServer` is left nil —
    controller-runtime's own lazy default still lets a `Controller` call
    `mgr.GetWebhookServer()`, just with no certificate provisioned.
- **Setup**: `SetupWithManager` is called on each `Controller`, in order,
  synchronously, before `ListenAndServe`. An error fails `New`, same as any
  other config validation today (e.g. `ErrAdminGroupRequired`).
- **Start** (`ListenAndServe`, once the listener is bound): `mgr.Start(mgrCtx)`
  runs in its own tracked goroutine, `mgrCtx` derived from
  `context.Background()`. A `Controller`'s own "start" behavior is whatever
  controller-runtime already provides — a registered reconciler's
  `Reconcile` fires on watch events, and/or a `mgr.Add(runnable)` runnable's
  `Start(ctx) error` runs directly. With leader election enabled, a non-webhook
  `Runnable`/reconciler doesn't actually run until this replica wins the
  election — `ListenAndServe` itself is unaffected; the API server serves
  immediately regardless. Webhook handlers always serve regardless of
  leadership (`webhook.Server.NeedLeaderElection()` returns `false`).
- **Stop** (`Shutdown`, **before** the existing `cancel()`/`httpServer.Shutdown`
  sequence): cancel `mgrCtx`, then block on the manager's `Start` goroutine
  returning, bounded by the caller's `ctx`. Only once the manager has fully
  stopped does libkapi proceed to close the HTTP listener. This ordering is
  the entire point: controller-runtime's `Manager.Start` doesn't return
  until every registered `Runnable`'s `Start` has returned, and the
  idiomatic pattern for a `Runnable` is to watch `ctx.Done()`, do cleanup
  with a *fresh* context, and then return — kontinuum's heartbeat runnable
  deletes its own object this way. Because libkapi holds the listener open
  for that whole window, the cleanup call actually lands instead of racing
  a closed socket. The webhook listener and leader-election release both
  need no separate handling — they're just more of the manager's own
  internally tracked components, stopped by the same `mgr.Start` returning.

There is deliberately no separate `Stop` method on `Controller` — `Start`
plus `ctx` cancellation is controller-runtime's own convention, and
introducing a second, libkapi-specific hook alongside it would just be two
ways to express the same thing.

A running `Controller`'s error (from `Reconcile` or a `Runnable.Start`) is
logged, not fatal to the server — a controller bug shouldn't take down the
API server it's attached to.

## Example (kontinuum's server registry)

```go
scheme := runtime.NewScheme()
v1alpha1.AddToScheme(scheme)

server, err := libkapi.New(ctx,
	libkapi.WithAddr(cfg.Server.Addr),
	libkapi.WithStorage(cfg.Server.Storage),
	libkapi.WithScheme(scheme),
	libkapi.WithController(registry.NewController(registry.Config{
		Role:   role,
		Region: cfg.Server.Region,
		Zone:   cfg.Server.Zone,
		Logger: logger,
	})),
)
```

`registry.Controller` (already implemented in kontinuum's `pkg/registry`)
satisfies `libkapi.Controller` purely structurally — no adapter code.
Notably, this example uses neither `WithLeaderElection` nor
`WithWebhookServer` — every kontinuum replica must register/heartbeat/delete
its own object independently, and it has no webhooks. A future `Controller`
that does need either would add:

```go
libkapi.WithLeaderElection(libkapi.LeaderElectionConfig{ID: "my-controller"}),
libkapi.WithWebhookServer(libkapi.WebhookConfig{}),
```

(`WebhookConfig{}` is enough on its own — `DNSNames` defaults to
`[]string{"localhost"}`, since the webhook server only ever answers calls
from the API server built into this same process.)

## Testing

- A registered `Controller`'s `SetupWithManager` receives a `Manager` whose
  client can actually create/get a real object — proves the loopback
  identity is privileged and working.
- A `Runnable` registered via `mgr.Add` deletes that object on `ctx.Done()`
  using a fresh context, and this is observable as having completed
  *before* `Server.Shutdown` returns — proves the graceful-shutdown-window
  guarantee end to end.
- `Shutdown` doesn't hang forever if the passed-in `ctx` already carries a
  short deadline.
- Two `Server`s (or two `Manager`s) configured with the same
  `LeaderElectionConfig.ID` against the same backing storage: only one's
  registered `Controller` becomes active at a time — proves the
  `coordination.k8s.io` `Lease`-based lock actually works against libkapi's
  own API.
- A `Controller` registering a handler via
  `mgr.GetWebhookServer().Register(...)` in `SetupWithManager` is reachable
  over HTTPS on `WebhookConfig.Port` using the auto-generated certificate —
  proves the webhook listener starts and is wired into the same start/stop
  lifecycle as everything else.
- Calling `New` twice against the same `/tmp` cert path reuses the same
  certificate the second time (byte-identical `tls.crt`) instead of
  regenerating it.

## Non-goals (this version)

- No support for more than one `Manager` per `Server`.
- No certificate *rotation*: the self-signed webhook certificate is
  generated once, the first time `webhookCertDir` doesn't already have one,
  and reused across restarts after that — nothing regenerates it on expiry
  (~1 year, per `GenerateSelfSignedCertKey`) or lets the caller supply their
  own CA/cert instead (e.g. one issued by cert-manager). Both are natural
  follow-ups, not in this version.
- No conversion-webhook-specific helpers beyond what
  `mgr.GetWebhookServer()` already gives a `Controller` — registering one is
  the caller's own `SetupWithManager` code, same as any other webhook.
