package namespaces

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const NamespaceResource = "namespaces"

func NewNamespaceREST(storageConfig storagebackend.Config) (rest.Storage, error) {
	store, delete, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(NamespaceResource)),
		func() runtime.Object { return &corev1.Namespace{} },
		func() runtime.Object { return &corev1.NamespaceList{} },
		"/"+NamespaceResource,
	)
	if err != nil {
		return nil, err
	}
}
