package secrets

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
)

const SecretsResource = "secrets"

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
        NewFunc: func() runtime.Object { return &corev1.Secret{} },
        NewListFunc: func() runtime.Object { return &corev1.SecretList{} },
        // PredicateFunc: MatchMyResource,
        // DefaultQualifiedResource: metav1.GroupResource{Group: "all", Resource: NamespaceResource},
        Storage: dryRunnableStorage,
        // Other fields like KeyFunc, CreateStrategy, etc., must be set.
    }

	return restStore, nil
}
