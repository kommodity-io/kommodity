package secrets

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const SecretsResource = "secrets"

func NewSecretsREST(storageConfig storagebackend.Config) (rest.Storage, error) {
	store, delete, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(SecretsResource)),
		func() runtime.Object { return &corev1.Secret{} },
		func() runtime.Object { return &corev1.SecretList{} },
		"/"+SecretsResource,
	)
	if err != nil {
		return nil, err
	}
}
