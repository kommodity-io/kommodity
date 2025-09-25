// Package configmaps implements the storage strategy towards kine for the core v1 ConfigMap resource.
package configmaps

import (
	"context"
	"fmt"
	"path"
	"reflect"

	corev1 "k8s.io/api/core/v1"

	"github.com/kommodity-io/kommodity/pkg/storage"

	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	apistorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const configMapResource = "configmaps"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"cm"}
}

// NewConfigMapsREST creates a REST interface for corev1 ConfigMap resource.
func NewConfigMapsREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(configMapResource)),
		func() runtime.Object { return &corev1.ConfigMap{} },
		func() runtime.Object { return &corev1.ConfigMapList{} },
		"/"+configMapResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	configMapStrategy := configMapStrategy{
		scheme: scheme,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.ConfigMap{} },
		NewListFunc:   func() runtime.Object { return &corev1.ConfigMapList{} },
		PredicateFunc: ConfigMapPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + configMapResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+configMapResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: configMapStrategy,
		UpdateStrategy: configMapStrategy,
		DeleteStrategy: configMapStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ConfigMapPredicateFunc returns a selection predicate for filtering ConfigMap objects.
func ConfigMapPredicateFunc(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a ConfigMap object.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return nil, nil, storage.ErrObjectIsNotAConfigMap
	}

	return labels.Set(configMap.Labels), SelectableFields(configMap), nil
}

// SelectableFields returns a field set that can be used for filter selection.
func SelectableFields(obj *corev1.ConfigMap) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return "", storage.ErrObjectIsNotAConfigMap
	}

	return configMap.Name, nil
}

// configMapStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/configmap/strategy.go
type configMapStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = configMapStrategy{}
var _ rest.RESTUpdateStrategy = configMapStrategy{}
var _ rest.RESTDeleteStrategy = configMapStrategy{}
var _ rest.NamespaceScopedStrategy = configMapStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (configMapStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate sets defaults for new objects.
func (configMapStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {}

// WarningsOnCreate returns warnings for the creation of the given object.
func (configMapStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate sets defaults for updated objects.
func (configMapStrategy) PrepareForUpdate(_ context.Context, _ runtime.Object, _ runtime.Object) {}

// WarningsOnUpdate returns warnings for the given update.
func (configMapStrategy) WarningsOnUpdate(_ context.Context, _ runtime.Object, _ runtime.Object) []string {
	return nil
}

// PrepareForDelete clears fields before deletion.
func (configMapStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (configMapStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAConfigMap.Error())}
	}

	return validateConfigMap(configMap)
}

// ValidateUpdate validates updated objects.
func (configMapStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newConfigMap, success := obj.(*corev1.ConfigMap)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAConfigMap.Error())}
	}

	oldConfigMap, success := old.(*corev1.ConfigMap)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAConfigMap.Error())}
	}

	allErrs := field.ErrorList{}
	allErrs = append(
		allErrs,
		validation.ValidateObjectMetaUpdate(&newConfigMap.ObjectMeta, &oldConfigMap.ObjectMeta, field.NewPath("metadata"))...,
	)

	if oldConfigMap.Immutable != nil && *oldConfigMap.Immutable {
		if newConfigMap.Immutable == nil || !*newConfigMap.Immutable {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("immutable"), "field is immutable when `immutable` is set"))
		}

		if !reflect.DeepEqual(newConfigMap.Data, oldConfigMap.Data) {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("data"), "field is immutable when `immutable` is set"))
		}

		if !reflect.DeepEqual(newConfigMap.BinaryData, oldConfigMap.BinaryData) {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("binaryData"), "field is immutable when `immutable` is set"))
		}
	}

	allErrs = append(allErrs, validateConfigMap(newConfigMap)...)

	return allErrs
}

// validateConfigMap tests whether required fields in the ConfigMap are set.
func validateConfigMap(cfg *corev1.ConfigMap) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(
		allErrs,
		validation.ValidateObjectMeta(&cfg.ObjectMeta, true, validation.NameIsDNSSubdomain, field.NewPath("metadata"))...,
	)

	totalSize := 0

	for key, value := range cfg.Data {
		for _, msg := range utilvalidation.IsConfigMapKey(key) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("data").Key(key), key, msg))
		}
		// check if we have a duplicate key in the other bag
		if _, isValue := cfg.BinaryData[key]; isValue {
			msg := "duplicate of key present in binaryData"
			allErrs = append(allErrs, field.Invalid(field.NewPath("data").Key(key), key, msg))
		}

		totalSize += len(value)
	}

	for key, value := range cfg.BinaryData {
		for _, msg := range utilvalidation.IsConfigMapKey(key) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("binaryData").Key(key), key, msg))
		}

		totalSize += len(value)
	}

	if totalSize > corev1.MaxSecretSize {
		// pass back "" to indicate that the error refers to the whole object.
		allErrs = append(allErrs, field.TooLong(field.NewPath(""), "" /*unused*/, corev1.MaxSecretSize))
	}

	return allErrs
}

// Canonicalize normalizes objects.
func (configMapStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (configMapStrategy) AllowCreateOnUpdate() bool {
	return false
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (configMapStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// GenerateName generates a name using the given base string.
func (configMapStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object.
func (cms configMapStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := cms.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (cms configMapStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return cms.scheme.Recognizes(gvk)
}
