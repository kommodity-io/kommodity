package libkapi

import (
	"net/http"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	restclient "k8s.io/client-go/rest"
)

// Ctx exposes the resources available to a ServerFactory: the server's
// shared HTTP mux, its privileged loopback identity, its storage backend's
// etcd3-compatible endpoints, and - via GRPCServer - a lazily built gRPC
// server. New accessors can be added here over time without changing
// ServerFactory's signature, so extending what a factory can reach is never
// a breaking change.
type Ctx struct {
	mux              *http.ServeMux
	loopbackConfig   *restclient.Config
	grpcServer       *grpc.Server
	storageEndpoints []string
}

// HTTPMux returns the server's shared HTTP mux, for mounting additional
// routes alongside the built API server. Any request that doesn't match a
// registered route falls through to the API server's own handler.
func (c *Ctx) HTTPMux() *http.ServeMux {
	return c.mux
}

// LoopbackConfig returns a shallow copy of the built server's own
// privileged (system:masters-equivalent) identity - the same one
// Controller/Manager and libkapi's post-start/pre-shutdown hooks use - so a
// factory can build whatever client it needs (typed, dynamic, or
// controller-runtime) without requiring a full Controller/Manager for
// simple work. A copy, rather than the shared config itself, is returned so
// a factory customizing QPS/Burst/UserAgent/RateLimiter for its own client
// can't silently change those settings for every other consumer of the
// server's loopback identity - the same reasoning as configForGC's own copy.
func (c *Ctx) LoopbackConfig() *restclient.Config {
	return restclient.CopyConfig(c.loopbackConfig)
}

// StorageEndpoints returns the etcd3-compatible endpoints backing the
// server's storage - the same ones RESTOptionsGetters dial. For a Kine DSN
// scheme (postgres/mysql/sqlite/nats), this is the private endpoint of the
// in-process Kine gRPC server libkapi spawned; for etcd:// or unix://, the
// endpoint given to WithStorage directly. A clone, rather than the shared
// slice itself, is returned so a factory can't mutate the server's own
// endpoints - the same reasoning as LoopbackConfig's own copy.
func (c *Ctx) StorageEndpoints() []string {
	return slices.Clone(c.storageEndpoints)
}

// GRPCServer returns the server's gRPC server, building it (and
// registering reflection) the first time it's called. A built server only
// pays for the h2c/HTTP2 listener switch (see server.go's newHTTPServer) if
// some ServerFactory actually calls GRPCServer - one that only touches
// HTTPMux leaves the listener on plain HTTP/1.1. Factories run
// sequentially, so no locking is needed here.
func (c *Ctx) GRPCServer() *grpc.Server {
	if c.grpcServer == nil {
		c.grpcServer = grpc.NewServer()
		reflection.Register(c.grpcServer)
	}

	return c.grpcServer
}
