# libkapi

`libkapi` builds an embeddable, Kubernetes-API-compatible server: a generic
apiserver + apiextensions (CRD) server + aggregation layer, backed by
pluggable storage, with extension points for mounting your own HTTP handlers.

It is a standalone package — it does not depend on any other `kommodity`
package (no `pkg/config`, no `pkg/logging`) — so it can be used outside this
repository.

> [!WARNING]
> A built server has **no TLS and no authentication by default**: every
> request is treated as anonymous and always allowed. Use the [authentication
> options](#authentication) to configure authentication and authorization.
> Anyone who can reach the configured listener address without auth options
> has full read/write access to every resource the server exposes. Put a
> TLS-terminating, authenticating proxy in front of it before exposing it
> outside a trusted network.

## Limitations

`libkapi` is meant to eventually become the core that Kommodity itself runs
on, but it isn't ready to take on that role yet. Known fundamentals to
improve before Kommodity could adopt it:

- No TLS support — needs pluggable TLS before it can sit anywhere other than
  behind a fully trusted network. Authentication is now supported via
  [options](#authentication).
- Only each standard API group's GA version is wired (a few beta/alpha-only
  resources like `IPAddress`, `VolumeAttributesClass` aren't exposed yet) —
  see the [Supported API groups](#supported-api-groups) section.
- Relies on extending `k8s.io/kubernetes`'s `legacyscheme.Scheme` global
  singleton to reuse upstream registry storage providers, which constrains
  how a caller can customize the scheme for the groups it wires.
- No support yet for the extra API groups (`rbac`'s bootstrap-roles behavior
  aside) or CRD/webhook conventions Kommodity's providers currently depend on
  (e.g. `admissionregistration.k8s.io`).

## Installation

```sh
go get github.com/kommodity-io/kommodity/pkg/libkapi
```

## Usage

### Minimal server

```go
package main

import (
	"context"
	"log"
	"log/slog"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
)

func main() {
	ctx := context.Background()

	server, err := libkapi.New(ctx,
		libkapi.WithAddr(":8080"), // defaults to ":"+$PORT, then ":8080"
		libkapi.WithStorage("postgres://user:pass@localhost/kapi"), // see "Storage" below for other schemes
		libkapi.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
```

Once running, the server speaks the real Kubernetes API — point `kubectl` or
`client-go` at it:

```sh
kubectl --server=http://127.0.0.1:8080 get namespaces
kubectl --server=http://127.0.0.1:8080 apply -f my-deployment.yaml
```

### Mounting custom HTTP handlers

`WithHTTPHandlerFactory` lets you mount your own routes alongside the built
API server, on the same address and port. Pass it more than once to mount
several factories; they run in the order given:

```go
server, err := libkapi.New(ctx,
	libkapi.WithStorage("sqlite://local.db"),
	libkapi.WithHTTPHandlerFactory(func(mux *http.ServeMux) error {
		mux.HandleFunc("GET /healthz/custom", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		return nil
	}),
)
```

Every unmatched request still falls through to the Kubernetes API server's
own handler.

### Graceful shutdown

```go
ctx, cancel := context.WithCancel(context.Background())

server, err := libkapi.New(ctx, libkapi.WithStorage("sqlite://local.db"))
if err != nil {
	log.Fatal(err)
}

go func() {
	if err := server.ListenAndServe(ctx); err != nil {
		log.Println(err)
	}
}()

// ... later, on SIGTERM etc.
shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
defer shutdownCancel()

if err := server.Shutdown(shutdownCtx); err != nil {
	log.Println(err)
}
```

### Storage

`WithStorage` takes a polymorphic connection string, dispatched by URL scheme:

| Scheme                                                             | Behavior                                                                                                                                                                                                                |
| ------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `postgres://`, `postgresql://`, `mysql://`, `sqlite://`, `nats://` | Spawns an in-process [Kine](https://github.com/k3s-io/kine) endpoint (via `k3s-io/kine/pkg/endpoint.Listen`, as a goroutine — no subprocess) that translates the etcd3 client protocol into the given SQL/NATS dialect. |
| `etcd://host:port`                                                 | Talks directly to an already-running etcd3-compatible endpoint. Nothing is spawned.                                                                                                                                     |
| `unix:///path/to/socket`                                           | Talks directly to an already-running etcd3-compatible endpoint over a Unix socket (for example, a Kine instance you started yourself). Nothing is spawned.                                                              |

`Server.Shutdown` waits for any endpoint it spawned to actually finish before
returning.

### Logging

`WithLogger` is the single logging entry point: pass a `*slog.Logger` and all
log output — libkapi's own messages, klog output from the embedded
Kubernetes packages (apiserver, apiextensions-apiserver, kube-aggregator,
client-go), and `sigs.k8s.io/controller-runtime`'s own log output — is
routed through it. `New` bridges all of these to the slog logger
automatically via `logging.InstallKlogAdapter` and
`logging.InstallControllerRuntimeLogAdapter`, so the consumer never needs to
configure either separately.

```go
libkapi.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelInfo,
})))
```

The klog bridge is process-global (klog has a single backing logger), so the
last `New` call wins; callers that need a different klog configuration can
call `logging.InstallKlogAdapter(logger)` themselves (from
`github.com/kommodity-io/kommodity/pkg/libkapi/logging`), before or after
`New`.

The controller-runtime bridge is also process-global, but — unlike klog —
`sigs.k8s.io/controller-runtime/pkg/log.SetLogger` only takes effect on its
*first* call for the life of the process; later calls (including a second
`New`) are silently ignored by upstream. Without this bridge, any
`sigs.k8s.io/controller-runtime` usage in the process — not just libkapi's
own `WithController` manager, but also a consumer calling
`sigs.k8s.io/controller-runtime/pkg/client.New` directly — prints a one-time
`log.SetLogger(...) was never called; logs will not be displayed` warning
and discards its log output.

### Authentication

`New` accepts variadic `Option` values — the same `Option` type used for
addr/storage/logger/handlers/scheme — that configure authentication and
authorization. If no auth options are passed, the server defaults to
anonymous authentication and always-allow authorization.

```go
server, err := libkapi.New(ctx,
    libkapi.WithStorage("sqlite://local.db"),
    libkapi.WithOIDC(libkapi.OIDCConfig{
        IssuerURL:   "https://accounts.google.com",
        ClientID:    "my-client",
    }),
    libkapi.WithServiceAccount(libkapi.ServiceAccountConfig{
        // SigningKey nil = generate a 4096-bit RSA key in-memory
        KeyPersistence: &libkapi.KeyPersistenceConfig{
            Namespace:  "kommodity-system",
            SecretName: "service-account-signing-key",
        },
    }),
    libkapi.WithAdminAuthorizer(libkapi.AdminAuthorizerConfig{
        // Comma-delimited; a user in any listed group is allowed.
        AdminGroups: "my-admin-group,my-other-admin-group",
    }),
)
```

#### Available options

`New`'s `Option` type is shared by every option below, general and
auth-specific alike — pass any combination, in any order.

| Option                                           | Description                                                                                                                                                                                            |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `WithAddr(addr string)`                          | Sets the listener address, e.g. `":8080"`. Defaults to `":"+$PORT`, falling back to `":8080"` if `PORT` is unset or invalid.                                                                           |
| `WithStorage(storage string)`                    | Sets the storage connection string. See [Storage](#storage).                                                                                                                                           |
| `WithLogger(logger *slog.Logger)`                | Sets the logger for libkapi's own output and the bridged klog/gRPC/logrus output. See [Logging](#logging). Defaults to `slog.Default()`.                                                               |
| `WithHTTPHandlerFactory(f HTTPHandlerFactory)`   | Mounts an extra set of routes. See [Mounting custom HTTP handlers](#mounting-custom-http-handlers). Repeatable.                                                                                        |
| `WithScheme(scheme *runtime.Scheme)`             | Registers additional types beyond the standard API groups libkapi wires by default.                                                                                                                    |
| `WithTLS(cfg TLSConfig)`                         | Reserved for future use; passing it makes `New` return `ErrNotImplemented`.                                                                                                                            |
| `WithOIDC(cfg OIDCConfig)`                       | Adds an OIDC bearer-token authenticator. Fetches the issuer's discovery document at `IssuerURL/.well-known/openid-configuration` during `New`.                                                         |
| `WithServiceAccount(cfg ServiceAccountConfig)`   | Adds a ServiceAccount token authenticator and starts the SA token controller (issues tokens for ServiceAccounts). Optionally persists the signing key to a Secret and watches for key rotation.        |
| `WithAdminAuthorizer(cfg AdminAuthorizerConfig)` | Sets an authorizer that allows health endpoints (anonymous), `system:masters`, any group listed in the configured comma-delimited `AdminGroups`, and `system:serviceaccounts`; denies everything else. |
| `WithAuthorizer(a Authorizer)`                   | Sets a custom authorizer. Use this to plug in any `k8s.io/apiserver` authorizer.                                                                                                                       |
| `WithController(c Controller)`                   | Registers a `Controller` against the server's own privileged loopback identity. See [Controllers](#controllers). Repeatable.                                                                          |
| `WithLeaderElection(cfg LeaderElectionConfig)`   | Enables manager-wide leader election via a `coordination.k8s.io` `Lease`. See [Controllers](#controllers).                                                                                             |
| `WithWebhookServer(cfg WebhookConfig)`           | Enables the manager's webhook server, on its own port. See [Controllers](#controllers).                                                                                                                |
| `WithPostStartHook(fn PostStartHookFunc)`        | Registers a function to run once the listener is bound, before the controller manager starts. See [Post-start and pre-shutdown hooks](#post-start-and-pre-shutdown-hooks). Repeatable.                |
| `WithPreShutdownHook(fn PreShutdownHookFunc)`    | Registers a function to run once during `Shutdown`, before the listener closes. See [Post-start and pre-shutdown hooks](#post-start-and-pre-shutdown-hooks). Repeatable.                               |

#### OIDC

`OIDCConfig` fields:

| Field           | Default     | Description                           |
| --------------- | ----------- | ------------------------------------- |
| `IssuerURL`     | (required)  | OIDC issuer URL.                      |
| `ClientID`      | (required)  | OAuth 2.0 client ID (token audience). |
| `UsernameClaim` | `"email"`   | JWT claim used as the username.       |
| `GroupsClaim`   | `"groups"`  | JWT claim used for group membership.  |
| `ExtraScopes`   | none        | Additional OAuth 2.0 scopes.          |
| `SigningAlgs`   | `["RS256"]` | Accepted JWT signing algorithms.      |

#### ServiceAccount

`ServiceAccountConfig` fields:

| Field            | Default                       | Description                                                                                    |
| ---------------- | ----------------------------- | ---------------------------------------------------------------------------------------------- |
| `Issuer`         | `"kubernetes/serviceaccount"` | Issuer for SA tokens.                                                                          |
| `SigningKey`     | (generated)                   | RSA private key for token signing/verification. If nil, a 4096-bit key is generated in-memory. |
| `KeyPersistence` | nil                           | If set, the signing key is persisted to a Secret (see below).                                  |
| `RootCA`         | nil                           | CA certificate included in token secrets as `ca.crt`.                                          |
| `TokenGetter`    | (loopback)                    | Validates SA existence. If nil, built from the server's loopback client.                       |
| `SecretsGetter`  | (loopback)                    | Validates Secret existence. If nil, built from the server's loopback client.                   |

`KeyPersistenceConfig` fields:

| Field                   | Default         | Description                                                                                                                          |
| ----------------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `Namespace`             | (required)      | Namespace for the signing key Secret. Created if it doesn't exist.                                                                   |
| `SecretName`            | (required)      | Name of the signing key Secret.                                                                                                      |
| `TokenSecretsNamespace` | `"kube-system"` | Namespace where SA token secrets are listed for rotation.                                                                            |
| `OnTokenRotated`        | nil             | Callback called for each SA token secret rotated during key rotation. Use this for side effects like updating autoscaler ConfigMaps. |

#### Security posture

| Options passed                                          | Authenticator                            | Authorizer                    |
| ------------------------------------------------------- | ---------------------------------------- | ----------------------------- |
| None                                                    | anonymous                                | always-allow                  |
| Auth options, no `WithAuthorizer`/`WithAdminAuthorizer` | union of strategies + anonymous fallback | always-allow (logged warning) |
| Auth options + `WithAuthorizer`/`WithAdminAuthorizer`   | union of strategies + anonymous fallback | caller's authorizer           |

### Controllers

`WithController` registers reconcilers and/or runnables against a single
[controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
`Manager` that libkapi builds, owns, and runs for the life of the `Server`,
using the server's own privileged (`system:masters`-equivalent) loopback
identity — the same one libkapi uses internally for its own SA token
controller and signing-key rotation. `SetupWithManager` is called once per
`Controller`, synchronously, during `New` — before the server starts
serving, so it should only *register* work (reconcilers via
`ctrl.NewControllerManagedBy(mgr)`, `Runnable`s via `mgr.Add`, webhook
handlers via `mgr.GetWebhookServer().Register`); making an actual API call
via `mgr.GetClient()` at that point fails, since nothing is listening on the
network yet. Do that from a `Runnable`'s `Start` or a reconciler's
`Reconcile` instead — both only run after `ListenAndServe` binds the
listener.

```go
type Controller interface {
	SetupWithManager(mgr Manager) error
}
```

`Manager` is a re-export of `ctrl.Manager` — implementing `Controller`
doesn't require importing `sigs.k8s.io/controller-runtime` directly.

```go
server, err := libkapi.New(ctx,
	libkapi.WithAddr(cfg.Addr),
	libkapi.WithStorage(cfg.Storage),
	libkapi.WithScheme(scheme), // register MyType's GVK first
	libkapi.WithController(myController),
)
```

A registered `Controller`'s error (from `Reconcile` or a `Runnable.Start`)
is logged, not fatal to the server. On `Shutdown`, the manager is stopped
- and given a real chance to finish its own cleanup - *before* the API
server's listener closes: a `Runnable` that watches `ctx.Done()`, does
cleanup with a fresh `context.Background()`-derived context, and then
returns (e.g. deleting its own registered object) will have that call
actually land instead of racing a closed socket.

#### Leader election

`WithLeaderElection` enables manager-wide leader election backed by a
`coordination.k8s.io/v1` `Lease`, using the server's own privileged loopback
identity. Without it (the default), every registered `Controller` runs
unmodified on every replica — they must be written to tolerate that.

| Field       | Default     | Description                                     |
| ----------- | ----------- | ------------------------------------------------ |
| `ID`        | (required)  | Name of the `Lease` object contenders coordinate on. |
| `Namespace` | `"default"` | The `Lease`'s namespace.                          |

#### Webhooks

`WithWebhookServer` enables the manager's webhook server, bound to
`127.0.0.1` only. Any registered `Controller` can call
`mgr.GetWebhookServer().Register(path, handler)` in its own
`SetupWithManager` — no change to the `Controller` interface. The server
only ever needs to answer admission/conversion calls made by the API server
built into this same process, never a Service or any other host, so there's
no cluster networking to configure — `WebhookConfig{}` is a valid, complete
config on its own.

| Field      | Default       | Description                                                                                                       |
| ---------- | ------------- | ------------------------------------------------------------------------------------------------------------------ |
| `Port`     | `9443`        | Port the webhook server listens on.                                                                                |
| `DNSNames` | `["localhost"]` | Subject Alternative Names embedded in the self-signed certificate `New` generates. The default matches the only hostname a caller dialing `127.0.0.1` from this host would ever use. |

`New` provisions a self-signed serving certificate on startup at the fixed
path `os.TempDir()/k8s-webhook-server/serving-certs` (controller-runtime's
own default `CertDir`) **only if one isn't already there** — restarts reuse
the same certificate instead of invalidating whatever `caBundle` was
registered on a `Validating`/`MutatingWebhookConfiguration`. libkapi does
not rotate this certificate or support supplying your own CA — both are
natural follow-ups, not implemented in this version.

### Post-start and pre-shutdown hooks

`WithPostStartHook` and `WithPreShutdownHook` are a lighter-weight
alternative to [`WithController`](#controllers) for simple background work
(e.g. a heartbeat loop) that needs the server's privileged loopback identity
but not a full `Controller`/`Manager` — no scheme registration, no
reconciler/`Runnable` boilerplate, just a function.

```go
type PostStartHookFunc func(ctx context.Context, loopbackConfig *rest.Config) error
type PreShutdownHookFunc func(ctx context.Context, loopbackConfig *rest.Config) error
```

- `WithPostStartHook` registrations run once, synchronously, in registration
  order, after `ListenAndServe`'s listener is bound and before the
  controller manager (if any) starts. A hook's error fails `ListenAndServe`
  with an ordinary Go error — unlike a failing `k8s.io/apiserver`-native
  post-start hook, which calls `klog.Fatal` and kills the process.
- `WithPreShutdownHook` registrations run once, synchronously, in
  registration order, during `Shutdown` — after the controller manager (if
  any) has stopped, but before the listener closes, so a hook still has a
  real chance to make one last privileged API call. Bounded by `Shutdown`'s
  own `ctx`, the same way the controller manager's stop is: a hook that
  ignores `ctx` only delays `Shutdown`, it never hangs it forever. A hook's
  error is logged, not fatal.

Both are passed the same privileged (`system:masters`-equivalent)
`loopbackConfig` `Controller`/`Manager` use internally, so a hook can build
whatever client it needs (`kubernetes.NewForConfig`, `client.New`, etc.).

### Supported API groups

The following standard Kubernetes API groups are wired using upstream
`k8s.io/kubernetes` registry storage providers — not hand-written REST
storage — at each group's GA version:

- `(core) v1` — `Namespace`, `Secret`, `ConfigMap`, `ServiceAccount`, `Event`, `ResourceQuota`(no`Pod`, `Service`, `Node`, `PersistentVolume(Claim)`, `ReplicationController`, or `PodTemplate` — those come from a much heavier upstream provider that also wires kubelet clients and Service IP/port allocators, out of scope for this library)
- `apps/v1` — `Deployment`, `StatefulSet`, `DaemonSet`, `ReplicaSet`, `ControllerRevision`
- `batch/v1` — `Job`, `CronJob`
- `rbac.authorization.k8s.io/v1` — `Role`, `RoleBinding`, `ClusterRole`, `ClusterRoleBinding`
- `networking.k8s.io/v1` — `NetworkPolicy`, `Ingress`, `IngressClass`
- `storage.k8s.io/v1` — `StorageClass`, `VolumeAttachment`, `CSINode`, `CSIDriver`, `CSIStorageCapacity`
- `coordination.k8s.io/v1` — `Lease` (used by `WithLeaderElection`; see [Controllers](#controllers))

Plus full `CustomResourceDefinition` support via `apiextensions-apiserver`
and API aggregation via `kube-aggregator`.

Only each group's GA version is wired; a few beta/alpha-only resources
(for example `networking.k8s.io/v1beta1`'s `IPAddress`, `storage.k8s.io`'s
`VolumeAttributesClass`) are not exposed. Groups beyond the seven listed
above (`admissionregistration.k8s.io`, `authorization.k8s.io`,
`discovery.k8s.io`, and so on) are not wired at all — a caller needing more
would need to extend `libkapi` itself.

## Reference

### `func New(ctx context.Context, opts ...Option) (*Server, error)`

Builds a full generic apiserver + apiextensions (CRD) server + aggregation
layer, wired to the standard Kubernetes API groups and backed by the storage
configured via `WithStorage`, plus any caller-supplied HTTP handlers. The
server is **not** started until `ListenAndServe` is called.

`opts` configure everything — see [Available options](#available-options).
If no auth options are passed, the server defaults to anonymous
authentication and always-allow authorization.

### `func logging.InstallKlogAdapter(logger *slog.Logger)`

Bridges klog output to the consumer's slog logger so that the embedded
Kubernetes packages route their log output through `logger` instead of
klog's default stderr writer. Called automatically by `New`; also safe to
call independently. If `logger` is nil, `slog.Default()` is used. The
bridge is process-global (klog has a single backing logger).

### `func logging.InstallControllerRuntimeLogAdapter(logger *slog.Logger)`

Bridges `sigs.k8s.io/controller-runtime`'s global logr sink to the
consumer's slog logger, so any controller-runtime usage in the process —
`WithController`'s manager, or a consumer calling
`sigs.k8s.io/controller-runtime/pkg/client.New` directly — logs through
`logger` instead of printing a one-time `log.SetLogger(...) was never
called` warning and discarding its output. Called automatically by `New`.
If `logger` is nil, `slog.Default()` is used. The bridge is process-global
and takes effect only on its first call for the life of the process — see
[Logging](#logging).

### `type TLSConfig struct`

Reserved for future external TLS support. Not implemented in this version;
exists so the public API won't need a breaking change once TLS support is
added.

| Field      | Type     |
| ---------- | -------- |
| `CertFile` | `string` |
| `KeyFile`  | `string` |

### `type HTTPHandlerFactory func(*http.ServeMux) error`

libkapi's extension-point type. Each factory passed via
`WithHTTPHandlerFactory` is called with the server's shared `*http.ServeMux`
during `New`; register whatever routes you need on it. Any request that
doesn't match a registered route falls through to the built API server's own
handler.

### `type Server struct`

A built, not-yet-started libkapi server. Construct one with `New`.

#### `func (s *Server) ListenAndServe(ctx context.Context) error`

Binds the listener and blocks until `ctx` is canceled, `Shutdown` is called,
or an unrecoverable error occurs. Returns `ErrServerAlreadyStarted` if called
more than once.

#### `func (s *Server) Shutdown(ctx context.Context) error`

Gracefully stops the HTTP listener, the apiserver's background run loop, and
(if `WithStorage` spawned one) the embedded Kine endpoint, waiting for
each to actually finish. Returns `ErrServerNotStarted` if `ListenAndServe`
was never called.

### Errors

| Sentinel                         | Returned when                                                                                                     |
| -------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `ErrUnsupportedConnectionScheme` | `WithStorage`'s scheme has no known storage backend.                                                              |
| `ErrEmptyConnectionString`       | `WithStorage` is empty or never called.                                                                           |
| `ErrEmptyStorageEndpoint`        | An `etcd://` or `unix://` connection string has no host or path.                                                  |
| `ErrGroupVersionNotRegistered`   | A standard API group has no version registered in the scheme (should not happen with the default scheme).         |
| `ErrServerAlreadyStarted`        | `ListenAndServe` is called more than once on the same `*Server`.                                                  |
| `ErrServerNotStarted`            | `Shutdown` is called before `ListenAndServe`.                                                                     |
| `ErrNotImplemented`              | `WithTLS` is used.                                                                                                |
| `ErrOIDCIssuerRequired`          | `WithOIDC` is called with an empty `IssuerURL`.                                                                   |
| `ErrOIDCClientIDRequired`        | `WithOIDC` is called with an empty `ClientID`.                                                                    |
| `ErrAdminGroupRequired`          | `WithAdminAuthorizer` is called with an `AdminGroups` that contains no non-empty group after splitting on commas. |
| `ErrLeaderElectionIDRequired`    | `WithLeaderElection` is called with an empty `ID`.                                                                |
