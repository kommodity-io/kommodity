// Package memory provides in-memory storage related utilities.
package memory

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
)

// ResourceHandlerProvider is a function that returns a REST storage provider for a given resource.
type ResourceHandlerProvider func(*runtime.Scheme, generic.RESTOptionsGetter) (rest.Storage, error)

// NewJSONBLOBStorageProvider creates a REST storage provider for JSON BLOB resources. The data
// is store in an in-memory map, and the storage is not persistent. It serves as a reference
// implementation that can be used for testing or development purposes.
func NewJSONBLOBStorageProvider(obj resource.Object) ResourceHandlerProvider {
	return func(scheme *runtime.Scheme, _ generic.RESTOptionsGetter) (rest.Storage, error) {
		groupResource := obj.GetGroupVersionResource().GroupResource()

		codec, _, err := storage.NewStorageCodec(storage.StorageCodecConfig{
			StorageMediaType:  runtime.ContentTypeJSON,
			StorageSerializer: serializer.NewCodecFactory(scheme),
			StorageVersion:    scheme.PrioritizedVersionsForGroup(obj.GetGroupVersionResource().Group)[0],
			MemoryVersion:     scheme.PrioritizedVersionsForGroup(obj.GetGroupVersionResource().Group)[0],
			Config:            storagebackend.Config{}, // useless fields..
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create storage codec: %w", err)
		}

		return NewJSONBLOBREST(
			groupResource,
			codec,
			obj.NamespaceScoped(),
			obj.New,
			obj.NewList,
		), nil
	}
}
