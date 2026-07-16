# libkapi

`libkapi` builds an embeddable, Kubernetes-API-compatible server: a generic
apiserver + apiextensions (CRD) server + aggregation layer, backed by
pluggable storage, with extension points for mounting your own HTTP handlers.

It is a standalone package — it does not depend on any other `kommodity`
package (no `pkg/config`, no `pkg/logging`) — so it can be used outside this
repository.

> [!WARNING]
> A built server has **no TLS and no authentication by default**: every
> request is treated as anonymous and always allowed. Anyone who can reach
> the configured listener address has full read/write access to every
> resource the server exposes. Put a TLS-terminating, authenticating proxy in
> front of it before exposing it outside a trusted network.

## Limitations

`libkapi` is meant to eventually become the core that Kommodity itself runs
on, but it isn't ready to take on that role yet. Known fundamentals to
improve before Kommodity could adopt it:

- No TLS and no authentication by default (anonymous + always-allow) — needs
  pluggable authn/authz before it can sit anywhere other than behind a fully
  trusted network.
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

### `func New(ctx context.Context, cfg Config) (*Server, error)`

Builds a full generic apiserver + apiextensions (CRD) server + aggregation
layer, wired to the standard Kubernetes API groups and backed by
`cfg.Storage`, plus any caller-supplied HTTP handlers. The server is **not**
started until `ListenAndServe` is called.

### `type Config struct`

| Field | Type | Description |
| --- | --- | --- |
| `Addr` | `string` | Listener address, e.g. `":8080"`. Defaults to `":"+$PORT`, falling back to `":8080"` if `PORT` is unset or invalid. |
| `Storage` | `string` | Polymorphic connection string. See [Storage](#storage). |
| `Logger` | `*slog.Logger` | Receives libkapi's internal log output. Defaults to `slog.Default()` if nil. |
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
| `ErrGroupVersionNotRegistered` | A standard API group has no version registered in the scheme (should not happen with the default scheme). |
| `ErrServerAlreadyStarted` | `ListenAndServe` is called more than once on the same `*Server`. |
| `ErrServerNotStarted` | `Shutdown` is called before `ListenAndServe`. |
| `ErrNotImplemented` | `Config.TLS` is non-nil. |
