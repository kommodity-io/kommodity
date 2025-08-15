package kine

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

// RESTOptionsGetter is a genericregistry.RESTOptionsGetter backed by a storagebackend.Config.
type RESTOptionsGetter struct {
	StorageConfig storagebackend.Config
	Options       *options.StorageFactoryRestOptionsFactory
}

// GetRESTOptions returns RESTOptions for the given resource.
func (g *RESTOptionsGetter) GetRESTOptions(resource schema.GroupResource,
	example runtime.Object) (genericregistry.RESTOptions, error) {
	if g.Options != nil {
		options, err := g.Options.GetRESTOptions(resource, example)
		if err != nil {
			return genericregistry.RESTOptions{},
				fmt.Errorf("failed to get REST Options from RESTOptionsGetter.Options: %w", err)
		}

		return options, nil
	}

	return genericregistry.RESTOptions{
		StorageConfig:           g.StorageConfig.ForResource(resource),
		DeleteCollectionWorkers: 1,
		EnableGarbageCollection: true,
		Decorator:               genericregistry.UndecoratedStorage,
		ResourcePrefix:          resource.Resource,
	}, nil
}

// NewKineRESTOptionsGetter creates a new Kine-backed RESTOptionsGetter.
// Pass in the storagebackend.Config you already use for Namespaces.
//
//nolint:ireturn
func NewKineRESTOptionsGetter(cfg storagebackend.Config) genericregistry.RESTOptionsGetter {
	return &RESTOptionsGetter{StorageConfig: cfg}
}
