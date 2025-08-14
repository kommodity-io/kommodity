package namespaces

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const NamespaceResource = "namespaces"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (r *REST) NamespaceScoped() bool {
	return false // Namespaces are cluster-scoped
}

func NewNamespacesREST(storageConfig storagebackend.Config) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(NamespaceResource)),
		func() runtime.Object { return &corev1.Namespace{} },
		func() runtime.Object { return &corev1.NamespaceList{} },
		"/"+NamespaceResource,
	)
	if err != nil {
		return nil, err
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	restStore := &genericregistry.Store{
		NewFunc:     func() runtime.Object { return &corev1.Namespace{} },
		NewListFunc: func() runtime.Object { return &corev1.NamespaceList{} },
		// PredicateFunc: MatchMyResource,
		// DefaultQualifiedResource: metav1.GroupResource{Group: "all", Resource: NamespaceResource},
		Storage: dryRunnableStorage,
	}

	return &REST{restStore}, nil
}
