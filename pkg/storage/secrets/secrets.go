// Package secrets implements the storage strategy towards kine for the core v1 Secret resource.
package secrets

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"path"

	corev1 "k8s.io/api/core/v1"

	"github.com/kommodity-io/kommodity/pkg/storage"

	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	apistorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const secretResource = "secrets"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"sc"}
}

// NewSecretsREST creates a REST interface for corev1 Secret resource.
func NewSecretsREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(secretResource)),
		func() runtime.Object { return &corev1.Secret{} },
		func() runtime.Object { return &corev1.SecretList{} },
		"/"+secretResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	secretStrategy := secretStrategy{
		scheme: scheme,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.Secret{} },
		NewListFunc:   func() runtime.Object { return &corev1.SecretList{} },
		PredicateFunc: SecretPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + secretResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+secretResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: secretStrategy,
		UpdateStrategy: secretStrategy,
		DeleteStrategy: secretStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// SecretPredicateFunc returns a selection predicate for filtering Secret objects.
func SecretPredicateFunc(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a Secret object.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil, nil, storage.ErrObjectIsNotASecret
	}

	return labels.Set(secret.Labels), SelectableFields(secret), nil
}

// SelectableFields returns a field set that can be used for filter selection.
func SelectableFields(obj *corev1.Secret) fields.Set {
	objectMetaFieldsSet := generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
	secretSpecificFieldsSet := fields.Set{
		"type": string(obj.Type),
	}

	return generic.MergeFieldsSets(objectMetaFieldsSet, secretSpecificFieldsSet)
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return "", storage.ErrObjectIsNotASecret
	}

	return secret.Name, nil
}

// secretStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/secret/strategy.go
type secretStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = secretStrategy{}
var _ rest.RESTUpdateStrategy = secretStrategy{}
var _ rest.RESTDeleteStrategy = secretStrategy{}
var _ rest.NamespaceScopedStrategy = secretStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (secretStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate sets defaults for new objects.
func (secretStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {}

// WarningsOnCreate returns warnings for create operations.
func (secretStrategy) WarningsOnCreate(_ context.Context, obj runtime.Object) []string {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Printf("expected *corev1.Secret, got %T", obj)

		return []string{fmt.Sprintf("unexpected object type: %T", obj)}
	}

	return warningsForSecret(secret)
}

func warningsForSecret(secret *corev1.Secret) []string {
	var warnings []string

	if secret.Type == corev1.SecretTypeTLS {
		// Verify that the key matches the cert.
		_, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
		if err != nil {
			warnings = append(warnings, err.Error())
		}
	}

	return warnings
}

// PrepareForUpdate sets defaults for updated objects.
func (secretStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newSecret, success := obj.(*corev1.Secret)
	if !success {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	oldSecret, success := old.(*corev1.Secret)
	if !success {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	// this is weird, but consistent with what the validatedUpdate function used to do.
	if len(newSecret.Type) == 0 {
		newSecret.Type = oldSecret.Type
	}
}

// WarningsOnUpdate returns warnings for update operations.
func (secretStrategy) WarningsOnUpdate(_ context.Context, _, obj runtime.Object) []string {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	return warningsForSecret(secret)
}

// PrepareForDelete clears fields before deletion.
func (secretStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (secretStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	secretObject, ok := obj.(*corev1.Secret)
	if !ok {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	return validation.ValidateObjectMeta(
		&secretObject.ObjectMeta, true,
		storage.ValidateNonNullField,
		field.NewPath("metadata"),
	)
}

// ValidateUpdate validates updated objects.
func (secretStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	secretObject, success := obj.(*corev1.Secret)
	if !success {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	oldSecretObject, success := old.(*corev1.Secret)
	if !success {
		log.Printf("expected *corev1.Secret, got %T", obj)
	}

	return validation.ValidateObjectMetaUpdate(
		&secretObject.ObjectMeta,
		&oldSecretObject.ObjectMeta,
		field.NewPath("metadata"),
	)
}

// Canonicalize normalizes objects.
func (secretStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (secretStrategy) AllowCreateOnUpdate() bool {
	return false
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (secretStrategy) AllowUnconditionalUpdate() bool {
	return false
}

// GenerateName generates a name using the given base string.
func (secretStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object.
func (ss secretStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := ss.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (ss secretStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return ss.scheme.Recognizes(gvk)
}
