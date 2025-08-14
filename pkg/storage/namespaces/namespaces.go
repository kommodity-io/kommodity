package namespaces

import (
	"context"
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const NamespaceResource = "namespaces"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"ns"}
}

func NewNamespacesREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
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

	namespaceStrategy := NewNamespaceStrategy(scheme)

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.Namespace{} },
		NewListFunc:   func() runtime.Object { return &corev1.NamespaceList{} },
		PredicateFunc: NamespacePredicateFunc,
		KeyRootFunc:   func(ctx context.Context) string { return "/" + NamespaceResource },
		KeyFunc: func(ctx context.Context, name string) (string, error) {
			return path.Join("/"+NamespaceResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: namespaceStrategy,
		UpdateStrategy: namespaceStrategy,
		DeleteStrategy: namespaceStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// NamespacePredicateFunc returns a selection predicate for filtering Namespace objects
func NamespacePredicateFunc(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a Namespace object
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Namespace")
	}
	return labels.Set(ns.Labels), fields.Set{
		"metadata.name": ns.Name,
		"status.phase":  string(ns.Status.Phase),
	}, nil
}

// ObjectNameFunc returns the name of the object
func ObjectNameFunc(obj runtime.Object) (string, error) {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return "", fmt.Errorf("ObjectNameFunc: not a Namespace object")
	}
	return ns.Name, nil
}

// namespaceStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/namespace/strategy.go
type namespaceStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = namespaceStrategy{}
var _ rest.RESTUpdateStrategy = namespaceStrategy{}
var _ rest.RESTDeleteStrategy = namespaceStrategy{}
var _ rest.NamespaceScopedStrategy = namespaceStrategy{}

func NewNamespaceStrategy(scheme runtime.Scheme) namespaceStrategy {
	return namespaceStrategy{
		scheme: scheme,
	}
}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (namespaceStrategy) NamespaceScoped() bool {
	return false
}

// PrepareForCreate sets defaults for new objects
func (namespaceStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {}

// WarningsOnCreate returns warnings for create operations
func (namespaceStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

// PrepareForUpdate sets defaults for updated objects
func (namespaceStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newNamespace := obj.(*corev1.Namespace)
	oldNamespace := old.(*corev1.Namespace)
	newNamespace.Spec.Finalizers = oldNamespace.Spec.Finalizers
	newNamespace.Status = oldNamespace.Status
}

// WarningsOnUpdate returns warnings for update operations
func (namespaceStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}

// PrepareForDelete clears fields before deletion
func (namespaceStrategy) PrepareForDelete(ctx context.Context, obj runtime.Object) {}

// Validate validates new objects
func (namespaceStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return validation.ValidateObjectMeta(&obj.(*corev1.Namespace).ObjectMeta, false, validation.ValidateNamespaceName, field.NewPath("metadata"))
}

// ValidateUpdate validates updated objects
func (namespaceStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return validation.ValidateObjectMetaUpdate(&obj.(*corev1.Namespace).ObjectMeta, &old.(*corev1.Namespace).ObjectMeta, field.NewPath("metadata"))
}

// Canonicalize normalizes objects
func (namespaceStrategy) Canonicalize(obj runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update
func (namespaceStrategy) AllowCreateOnUpdate() bool {
	return false
}

// AllowUnconditionalUpdate determines if update can ignore resource version
func (namespaceStrategy) AllowUnconditionalUpdate() bool {
	return false
}

// GenerateName generates a name using the given base string
func (namespaceStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object
func (ns namespaceStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	return ns.scheme.ObjectKinds(obj)
}

// Recognizes returns true if this strategy handles the given GroupVersionKind
func (ns namespaceStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return ns.scheme.Recognizes(gvk)
}
