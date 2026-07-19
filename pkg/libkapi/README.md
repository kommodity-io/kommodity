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

	server, err := libkapi.New(ctx, libkapi.Config{
		Addr:    ":8080",                              // defaults to ":"+$PORT, then ":8080"
		Storage: "postgres://user:pass@localhost/kapi", // see "Storage" below for other schemes
		Logger:  slog.Default(),
	})
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

`Config.Handlers` lets you mount your own routes alongside the built API
server, on the same address and port:

```go
cfg := libkapi.Config{
	Storage: "sqlite://local.db",
	Handlers: []libkapi.HTTPHandlerFactory{
		func(mux *http.ServeMux) error {
			mux.HandleFunc("GET /healthz/custom", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			return nil
		},
	},
}
```

Every unmatched request still falls through to the Kubernetes API server's
own handler.

### Graceful shutdown

```go
ctx, cancel := context.WithCancel(context.Background())

server, err := libkapi.New(ctx, cfg)
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

`Config.Storage` is a polymorphic connection string, dispatched by URL scheme:

| Scheme | Behavior |
| --- | --- |
| `postgres://`, `postgresql://`, `mysql://`, `sqlite://`, `nats://` | Spawns an in-process [Kine](https://github.com/k3s-io/kine) endpoint (via `k3s-io/kine/pkg/endpoint.Listen`, as a goroutine — no subprocess) that translates the etcd3 client protocol into the given SQL/NATS dialect. |
| `etcd://host:port` | Talks directly to an already-running etcd3-compatible endpoint. Nothing is spawned. |
| `unix:///path/to/socket` | Talks directly to an already-running etcd3-compatible endpoint over a Unix socket (for example, a Kine instance you started yourself). Nothing is spawned. |

`Server.Shutdown` waits for any endpoint it spawned to actually finish before
returning.

### Logging

`Config.Logger` is the single logging entry point: pass a `*slog.Logger` and
all log output — libkapi's own messages **and** klog output from the embedded
Kubernetes packages (apiserver, apiextensions-apiserver, kube-aggregator,
client-go) — is routed through it. `New` bridges klog to the slog logger
automatically via `InstallKlogAdapter`, so the consumer never needs to
configure klog separately.

```go
cfg := libkapi.Config{
    Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })),
    // ...
}
```

The bridge is process-global (klog has a single backing logger), so the last
`New` call wins. Callers that need a different klog configuration can call
`libkapi.InstallKlogAdapter(logger)` themselves, before or after `New`.

### Authentication

`New` accepts variadic `Option` values that configure authentication and
authorization. If no options are passed, the server defaults to anonymous
authentication and always-allow authorization.

```go
server, err := libkapi.New(ctx, cfg,
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

| Option | Description |
| --- | --- |
| `WithOIDC(cfg OIDCConfig)` | Adds an OIDC bearer-token authenticator. Fetches the issuer's discovery document at `IssuerURL/.well-known/openid-configuration` during `New`. |
| `WithServiceAccount(cfg ServiceAccountConfig)` | Adds a ServiceAccount token authenticator and starts the SA token controller (issues tokens for ServiceAccounts). Optionally persists the signing key to a Secret and watches for key rotation. |
| `WithAdminAuthorizer(cfg AdminAuthorizerConfig)` | Sets an authorizer that allows health endpoints (anonymous), `system:masters`, any group listed in the configured comma-delimited `AdminGroups`, and `system:serviceaccounts`; denies everything else. |
| `WithAuthorizer(a Authorizer)` | Sets a custom authorizer. Use this to plug in any `k8s.io/apiserver` authorizer. |

#### OIDC

`OIDCConfig` fields:

| Field | Default | Description |
| --- | --- | --- |
| `IssuerURL` | (required) | OIDC issuer URL. |
| `ClientID` | (required) | OAuth 2.0 client ID (token audience). |
| `UsernameClaim` | `"email"` | JWT claim used as the username. |
| `GroupsClaim` | `"groups"` | JWT claim used for group membership. |
| `ExtraScopes` | none | Additional OAuth 2.0 scopes. |
| `SigningAlgs` | `["RS256"]` | Accepted JWT signing algorithms. |

#### ServiceAccount

`ServiceAccountConfig` fields:

| Field | Default | Description |
| --- | --- | --- |
| `Issuer` | `"kubernetes/serviceaccount"` | Issuer for SA tokens. |
| `SigningKey` | (generated) | RSA private key for token signing/verification. If nil, a 4096-bit key is generated in-memory. |
| `KeyPersistence` | nil | If set, the signing key is persisted to a Secret (see below). |
| `RootCA` | nil | CA certificate included in token secrets as `ca.crt`. |
| `TokenGetter` | (loopback) | Validates SA existence. If nil, built from the server's loopback client. |
| `SecretsGetter` | (loopback) | Validates Secret existence. If nil, built from the server's loopback client. |

`KeyPersistenceConfig` fields:

| Field | Default | Description |
| --- | --- | --- |
| `Namespace` | (required) | Namespace for the signing key Secret. Created if it doesn't exist. |
| `SecretName` | (required) | Name of the signing key Secret. |
| `TokenSecretsNamespace` | `"kube-system"` | Namespace where SA token secrets are listed for rotation. |
| `OnTokenRotated` | nil | Callback called for each SA token secret rotated during key rotation. Use this for side effects like updating autoscaler ConfigMaps. |

#### Security posture

| Options passed | Authenticator | Authorizer |
| --- | --- | --- |
| None | anonymous | always-allow |
| Auth options, no `WithAuthorizer`/`WithAdminAuthorizer` | union of strategies + anonymous fallback | always-allow (logged warning) |
| Auth options + `WithAuthorizer`/`WithAdminAuthorizer` | union of strategies + anonymous fallback | caller's authorizer |

### Supported API groups

The following standard Kubernetes API groups are wired using upstream
`k8s.io/kubernetes` registry storage providers — not hand-written REST
storage — at each group's GA version:

- `` (core) `v1` — `Namespace`, `Secret`, `ConfigMap`, `ServiceAccount`, `Event`, `ResourceQuota` (no `Pod`, `Service`, `Node`, `PersistentVolume(Claim)`, `ReplicationController`, or `PodTemplate` — those come from a much heavier upstream provider that also wires kubelet clients and Service IP/port allocators, out of scope for this library)
- `apps/v1` — `Deployment`, `StatefulSet`, `DaemonSet`, `ReplicaSet`, `ControllerRevision`
- `batch/v1` — `Job`, `CronJob`
- `rbac.authorization.k8s.io/v1` — `Role`, `RoleBinding`, `ClusterRole`, `ClusterRoleBinding`
- `networking.k8s.io/v1` — `NetworkPolicy`, `Ingress`, `IngressClass`
- `storage.k8s.io/v1` — `StorageClass`, `VolumeAttachment`, `CSINode`, `CSIDriver`, `CSIStorageCapacity`

Plus full `CustomResourceDefinition` support via `apiextensions-apiserver`
and API aggregation via `kube-aggregator`.

Only each group's GA version is wired; a few beta/alpha-only resources
(for example `networking.k8s.io/v1beta1`'s `IPAddress`, `storage.k8s.io`'s
`VolumeAttributesClass`) are not exposed. Groups beyond the six listed above
(`admissionregistration.k8s.io`, `authorization.k8s.io`,
`coordination.k8s.io`, `discovery.k8s.io`, and so on) are not wired at all —
a caller needing more would need to extend `libkapi` itself.

## Reference

### `func New(ctx context.Context, cfg Config, opts ...Option) (*Server, error)`

Builds a full generic apiserver + apiextensions (CRD) server + aggregation
layer, wired to the standard Kubernetes API groups and backed by
`cfg.Storage`, plus any caller-supplied HTTP handlers. The server is **not**
started until `ListenAndServe` is called.

`opts` configure authentication and authorization (see
[Authentication](#authentication)). If none are passed, the server defaults to
anonymous authentication and always-allow authorization.

### `func InstallKlogAdapter(logger *slog.Logger)`

Bridges klog output to the consumer's slog logger so that the embedded
Kubernetes packages route their log output through `logger` instead of
klog's default stderr writer. Called automatically by `New`; also safe to
call independently. If `logger` is nil, `slog.Default()` is used. The
bridge is process-global (klog has a single backing logger).

### `type Config struct`

| Field | Type | Description |
| --- | --- | --- |
| `Addr` | `string` | Listener address, e.g. `":8080"`. Defaults to `":"+$PORT`, falling back to `":8080"` if `PORT` is unset or invalid. |
| `Storage` | `string` | Polymorphic connection string. See [Storage](#storage). |
| `Logger` | `*slog.Logger` | Receives libkapi's internal log output, including klog output from the embedded Kubernetes packages. Defaults to `slog.Default()` if nil. See [Logging](#logging). |
| `TLS` | `*TLSConfig` | Reserved for future use. Must be `nil` in this version — setting it makes `New` return `ErrNotImplemented`. |
| `Handlers` | `[]HTTPHandlerFactory` | Mount additional routes onto the server's shared mux, alongside the built API server. |
| `Scheme` | `*runtime.Scheme` | Lets the caller register additional types beyond the standard API groups libkapi wires by default. |

### `type TLSConfig struct`

Reserved for future external TLS support. Not implemented in this version;
exists so the public API won't need a breaking change once TLS support is
added.

| Field | Type |
| --- | --- |
| `CertFile` | `string` |
| `KeyFile` | `string` |

### `type HTTPHandlerFactory func(*http.ServeMux) error`

libkapi's extension-point type. Each factory in `Config.Handlers` is called
with the server's shared `*http.ServeMux` during `New`; register whatever
routes you need on it. Any request that doesn't match a registered route
falls through to the built API server's own handler.

### `type Server struct`

A built, not-yet-started libkapi server. Construct one with `New`.

#### `func (s *Server) ListenAndServe(ctx context.Context) error`

Binds the listener and blocks until `ctx` is canceled, `Shutdown` is called,
or an unrecoverable error occurs. Returns `ErrServerAlreadyStarted` if called
more than once.

#### `func (s *Server) Shutdown(ctx context.Context) error`

Gracefully stops the HTTP listener, the apiserver's background run loop, and
(if `Config.Storage` spawned one) the embedded Kine endpoint, waiting for
each to actually finish. Returns `ErrServerNotStarted` if `ListenAndServe`
was never called.

### Errors

| Sentinel | Returned when |
| --- | --- |
| `ErrUnsupportedConnectionScheme` | `Config.Storage`'s scheme has no known storage backend. |
| `ErrEmptyConnectionString` | `Config.Storage` is empty. |
| `ErrEmptyStorageEndpoint` | An `etcd://` or `unix://` connection string has no host or path. |
| `ErrGroupVersionNotRegistered` | A standard API group has no version registered in the scheme (should not happen with the default scheme). |
| `ErrServerAlreadyStarted` | `ListenAndServe` is called more than once on the same `*Server`. |
| `ErrServerNotStarted` | `Shutdown` is called before `ListenAndServe`. |
| `ErrNotImplemented` | `Config.TLS` is non-nil. |
| `ErrOIDCIssuerRequired` | `WithOIDC` is called with an empty `IssuerURL`. |
| `ErrOIDCClientIDRequired` | `WithOIDC` is called with an empty `ClientID`. |
| `ErrAdminGroupRequired` | `WithAdminAuthorizer` is called with an `AdminGroups` that contains no non-empty group after splitting on commas. |
