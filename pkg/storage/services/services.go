// Package services implements the storage strategy towards kine for the core v1 Service resource.
package services

import (
	"context"
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"

	"github.com/kommodity-io/kommodity/pkg/logging"
	storageerr "github.com/kommodity-io/kommodity/pkg/storage"
	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const serviceResource = "services"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"svc"}
}

// NewServicesREST creates a REST interface for corev1 Namespace resource.
func NewServicesREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(serviceResource)),
		func() runtime.Object { return &corev1.Service{} },
		func() runtime.Object { return &corev1.ServiceList{} },
		"/"+serviceResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	serviceStrategy := serviceStrategy{
		scheme: scheme,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.Service{} },
		NewListFunc:   func() runtime.Object { return &corev1.ServiceList{} },
		PredicateFunc: ServicePredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + serviceResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+serviceResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: serviceStrategy,
		UpdateStrategy: serviceStrategy,
		DeleteStrategy: serviceStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ServicePredicateFunc returns a selection predicate for filtering Service objects.
func ServicePredicateFunc(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a Service object.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	service, ok := obj.(*corev1.Service)
	if !ok {
		return nil, nil, storageerr.ErrObjectIsNotAService
	}

	objectMetaFieldsSet := generic.ObjectMetaFieldsSet(&service.ObjectMeta, true)
	serviceSpecificFieldsSet := fields.Set{
		"spec.clusterIP": service.Spec.ClusterIP,
		"spec.type":      string(service.Spec.Type),
	}

	return service.Labels, generic.MergeFieldsSets(objectMetaFieldsSet, serviceSpecificFieldsSet), nil
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	service, ok := obj.(*corev1.Service)
	if !ok {
		return "", storageerr.ErrObjectIsNotAService
	}

	return service.Name, nil
}

// serviceStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/service/strategy.go
type serviceStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = serviceStrategy{}
var _ rest.RESTUpdateStrategy = serviceStrategy{}
var _ rest.RESTDeleteStrategy = serviceStrategy{}
var _ rest.NamespaceScopedStrategy = serviceStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (serviceStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate sets defaults for new objects.
func (serviceStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	service, success := obj.(*corev1.Service)
	if !success {
		logger := logging.FromContext(ctx)
		logger.Warn("Expected *corev1.Service", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	service.Status = corev1.ServiceStatus{}
}

// WarningsOnCreate returns warnings for create operations.
func (serviceStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate sets defaults for updated objects.
func (serviceStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	logger := logging.FromContext(ctx)

	newService, success := obj.(*corev1.Service)
	if !success {
		logger.Warn("Expected *corev1.Service for new object", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	oldService, success := old.(*corev1.Service)
	if !success {
		logger.Warn("Expected *corev1.Service for old object", zap.String("actual_type", fmt.Sprintf("%T", old)))
	}

	newService.Status = oldService.Status
}

// WarningsOnUpdate returns warnings for update operations.
func (serviceStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForDelete clears fields before deletion.
func (serviceStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (serviceStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	service, ok := obj.(*corev1.Service)
	if !ok {
		logger := logging.FromContext(ctx)
		logger.Warn("Expected *corev1.Service", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	return validation.ValidateObjectMeta(
		&service.ObjectMeta, false,
		validation.NameIsDNS1035Label,
		field.NewPath("metadata"),
	)
}

// ValidateUpdate validates updated objects.
func (serviceStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	logger := logging.FromContext(ctx)

	newService, success := obj.(*corev1.Service)
	if !success {
		logger.Warn("Expected *corev1.Service for new object", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	oldService, success := old.(*corev1.Service)
	if !success {
		logger.Warn("Expected *corev1.Service for old object", zap.String("actual_type", fmt.Sprintf("%T", old)))
	}

	allErrs := validation.ValidateObjectMetaUpdate(&newService.ObjectMeta,
		&oldService.ObjectMeta, field.NewPath("metadata"))

	return allErrs
}

// Canonicalize normalizes objects.
func (serviceStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (serviceStrategy) AllowCreateOnUpdate() bool {
	return true
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (serviceStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// GenerateName generates a name using the given base string.
func (serviceStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object.
func (ns serviceStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := ns.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (ns serviceStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return ns.scheme.Recognizes(gvk)
}
