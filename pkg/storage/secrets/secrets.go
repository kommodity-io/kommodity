package secrets

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const SecretsResource = "secrets"

var _ rest.ShortNamesProvider = &REST{}
var _ rest.NamespaceScopedStrategy = &REST{}

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (REST) NamespaceScoped() bool {
	return true
}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (REST) ShortNames() []string {
	return []string{"sc"}
}

func NewSecretsREST(storageConfig storagebackend.Config) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(SecretsResource)),
		func() runtime.Object { return &corev1.Secret{} },
		func() runtime.Object { return &corev1.SecretList{} },
		"/"+SecretsResource,
	)
	if err != nil {
		return nil, err
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	restStore := &genericregistry.Store{
		NewFunc:     func() runtime.Object { return &corev1.Secret{} },
		NewListFunc: func() runtime.Object { return &corev1.SecretList{} },
		// PredicateFunc: MatchMyResource,
		// DefaultQualifiedResource: metav1.GroupResource{Group: "all", Resource: NamespaceResource},
		Storage: dryRunnableStorage,
		// Other fields like KeyFunc, CreateStrategy, etc., must be set.
	}

	return &REST{restStore}, nil
}
