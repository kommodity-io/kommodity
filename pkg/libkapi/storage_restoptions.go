package libkapi

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

// restOptionsGetter is a genericregistry.RESTOptionsGetter backed by a
// storagebackend.Config. It is a small, dependency-free fork of
// pkg/kine/rest.go's RESTOptionsGetter: that type cannot be imported directly
// because it lives in the same Go package as pkg/kine/kine.go and
// pkg/kine/server.go, both of which import pkg/config and pkg/logging.
type restOptionsGetter struct {
	storageConfig storagebackend.Config
}

// GetRESTOptions returns RESTOptions for the given resource.
func (g *restOptionsGetter) GetRESTOptions(resource schema.GroupResource,
	_ runtime.Object) (genericregistry.RESTOptions, error) {
	return genericregistry.RESTOptions{
		StorageConfig:           g.storageConfig.ForResource(resource),
		DeleteCollectionWorkers: 1,
		EnableGarbageCollection: true,
		Decorator:               registry.StorageWithCacher(),
		ResourcePrefix:          resource.Resource,
	}, nil
}

// newRESTOptionsGetter builds a RESTOptionsGetter for an etcd3-compatible
// storage endpoint (a real etcd cluster, or a Kine instance speaking the
// etcd3 client protocol), using codec to (de)serialize the resources this
// getter is used for.
func newRESTOptionsGetter(codec runtime.Codec, endpoints []string) genericregistry.RESTOptionsGetter {
	return &restOptionsGetter{
		storageConfig: storagebackend.Config{
			Type:   storagebackend.StorageTypeETCD3,
			Prefix: "/registry",
			Codec:  codec,
			Transport: storagebackend.TransportConfig{
				ServerList: endpoints,
			},
		},
	}
}
