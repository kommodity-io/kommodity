// Package memory provides in-memory storage related utilities.
package memory

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
	builderrest "sigs.k8s.io/apiserver-runtime/pkg/builder/rest"
)

// NewJSONBLOBStorageProvider creates a REST storage provider for JSON BLOB resources. The data
// is store in an in-memory map, and the storage is not persistent. It serves as a reference
// implementation that can be used for testing or development purposes.
func NewJSONBLOBStorageProvider(obj resource.Object, rootPath string) builderrest.ResourceHandlerProvider {
	return func(scheme *runtime.Scheme, getter generic.RESTOptionsGetter) (rest.Storage, error) {
		gr := obj.GetGroupVersionResource().GroupResource()
		codec, _, err := storage.NewStorageCodec(storage.StorageCodecConfig{
			StorageMediaType:  runtime.ContentTypeJSON,
			StorageSerializer: serializer.NewCodecFactory(scheme),
			StorageVersion:    scheme.PrioritizedVersionsForGroup(obj.GetGroupVersionResource().Group)[0],
			MemoryVersion:     scheme.PrioritizedVersionsForGroup(obj.GetGroupVersionResource().Group)[0],
			Config:            storagebackend.Config{}, // useless fields..
		})
		if err != nil {
			return nil, err
		}

		return NewJSONBLOBREST(
			gr,
			codec,
			obj.NamespaceScoped(),
			obj.New,
			obj.NewList,
		), nil
	}
}
