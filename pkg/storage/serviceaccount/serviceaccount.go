// Package serviceaccount provides a storage interface for Kubernetes service accounts.
package serviceaccount

import (
	"context"
	"fmt"
	"path"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/storage"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const serviceAccountResource = "serviceaccounts"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"sa"}
}

// NewServiceAccountREST creates a REST interface for corev1 ServiceAccount resource.
func NewServiceAccountREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(serviceAccountResource)),
		func() runtime.Object { return &corev1.ServiceAccount{} },
		func() runtime.Object { return &corev1.ServiceAccountList{} },
		"/"+serviceAccountResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	serviceAccountStrategy := serviceAccountStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.ServiceAccount{} },
		NewListFunc:   func() runtime.Object { return &corev1.ServiceAccountList{} },
		PredicateFunc: storage.NamespacedPredicateFunc(),
		KeyRootFunc:   func(_ context.Context) string { return "/" + serviceAccountResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+serviceAccountResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: serviceAccountStrategy,
		UpdateStrategy: serviceAccountStrategy,
		DeleteStrategy: serviceAccountStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	serviceAccount, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		return "", storage.ErrObjectIsNotAServiceAccount
	}

	return serviceAccount.Name, nil
}

// serviceAccountStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/serviceaccount/strategy.go
//
//nolint:lll
type serviceAccountStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = serviceAccountStrategy{}
var _ rest.RESTUpdateStrategy = serviceAccountStrategy{}
var _ rest.RESTDeleteStrategy = serviceAccountStrategy{}
var _ rest.NamespaceScopedStrategy = serviceAccountStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (serviceAccountStrategy) NamespaceScoped() bool {
	return true
}

func (serviceAccountStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	serviceAccountObject, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAServiceAccount, obj))

		return
	}

	cleanSecretReferences(serviceAccountObject)
}

func (serviceAccountStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	serviceAccountObject, success := obj.(*corev1.ServiceAccount)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAServiceAccount.Error())}
	}

	return validation.ValidateObjectMeta(
		&serviceAccountObject.ObjectMeta,
		true,
		validation.ValidateServiceAccountName,
		field.NewPath("metadata"))
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (serviceAccountStrategy) WarningsOnCreate(_ context.Context, obj runtime.Object) []string {
	serviceAccountObject, success := obj.(*corev1.ServiceAccount)
	if !success {
		return []string{storage.ExpectedGot(storage.ErrObjectIsNotAServiceAccount, obj)}
	}

	return warnIfHasEnforceMountableSecretsAnnotation(serviceAccountObject, nil)
}

// Canonicalize normalizes the object after validation.
func (serviceAccountStrategy) Canonicalize(_ runtime.Object) {}

func (serviceAccountStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (serviceAccountStrategy) PrepareForUpdate(ctx context.Context, obj, _ runtime.Object) {
	logger := logging.FromContext(ctx)

	serviceAccountObject, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAServiceAccount, obj))

		return
	}

	cleanSecretReferences(serviceAccountObject)
}

func (serviceAccountStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newServiceAccountObject, success := obj.(*corev1.ServiceAccount)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAServiceAccount.Error())}
	}

	oldServiceAccountObject, success := old.(*corev1.ServiceAccount)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAServiceAccount.Error())}
	}

	allErrors := validation.ValidateObjectMetaUpdate(
		&newServiceAccountObject.ObjectMeta,
		&oldServiceAccountObject.ObjectMeta,
		field.NewPath("metadata"))

	allErrors = append(allErrors, validation.ValidateObjectMeta(
		&newServiceAccountObject.ObjectMeta,
		true,
		validation.ValidateServiceAccountName,
		field.NewPath("metadata"))...)

	return allErrors
}

// WarningsOnUpdate returns warnings for the given update.
func (serviceAccountStrategy) WarningsOnUpdate(_ context.Context, obj, old runtime.Object) []string {
	newServiceAccountObject, success := obj.(*corev1.ServiceAccount)
	if !success {
		return []string{storage.ExpectedGot(storage.ErrObjectIsNotAServiceAccount, obj)}
	}

	oldServiceAccountObject, success := old.(*corev1.ServiceAccount)
	if !success {
		return []string{storage.ExpectedGot(storage.ErrObjectIsNotAServiceAccount, old)}
	}

	return warnIfHasEnforceMountableSecretsAnnotation(newServiceAccountObject, oldServiceAccountObject)
}

func (serviceAccountStrategy) AllowUnconditionalUpdate() bool {
	return true
}

func cleanSecretReferences(serviceAccount *corev1.ServiceAccount) {
	for i, secret := range serviceAccount.Secrets {
		serviceAccount.Secrets[i] = corev1.ObjectReference{Name: secret.Name}
	}
}

func warnIfHasEnforceMountableSecretsAnnotation(newServiceAccount, oldServiceAccount *corev1.ServiceAccount) []string {
	if oldServiceAccount != nil {
		_, ok := oldServiceAccount.Annotations["kubernetes.io/enforce-mountable-secrets"]
		if ok {
			// skip warning if request isn't newly setting the annotation
			return nil
		}
	}

	_, ok := newServiceAccount.Annotations["kubernetes.io/enforce-mountable-secrets"]
	if ok {
		return []string{"metadata.annotations[kubernetes.io/enforce-mountable-secrets]: " +
			"deprecated in v1.32+; prefer separate namespaces to isolate access to mounted secrets"}
	}

	return nil
}
