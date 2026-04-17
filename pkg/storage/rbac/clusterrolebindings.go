package rbac

import (
	"context"
	"fmt"
	"path"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/storage"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	k8spath "k8s.io/apimachinery/pkg/api/validation/path"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/kubernetes/pkg/apis/rbac"
)

const clusterRoleBindingResource = "clusterrolebindings"

// NewClusterRoleBindingREST creates a REST interface for rbacv1 ClusterRoleBinding resource.
//
//nolint:dupl // Similar to pkg/storage/rbac/clusterroles.go::NewClusterRoleREST but not identical.
func NewClusterRoleBindingREST(
	storageConfig storagebackend.Config,
	scheme runtime.Scheme,
) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(clusterRoleBindingResource)),
		func() runtime.Object { return &rbacv1.ClusterRoleBinding{} },
		func() runtime.Object { return &rbacv1.ClusterRoleBindingList{} },
		"/"+clusterRoleBindingResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	clusterRoleBindingStrategy := clusterRoleBindingStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &rbacv1.ClusterRoleBinding{} },
		NewListFunc:   func() runtime.Object { return &rbacv1.ClusterRoleBindingList{} },
		PredicateFunc: storage.PredicateFunc(ClusterRoleBindingGetAttrs),
		KeyRootFunc:   func(_ context.Context) string { return "/" + clusterRoleBindingResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+clusterRoleBindingResource, name), nil
		},
		ObjectNameFunc: ObjectNameFuncClusterRoleBinding,
		CreateStrategy: clusterRoleBindingStrategy,
		UpdateStrategy: clusterRoleBindingStrategy,
		DeleteStrategy: clusterRoleBindingStrategy,
		Storage:        dryRunnableStorage,
	}

	return restStore, nil
}

// ClusterRoleBindingGetAttrs returns labels and fields for a ClusterRoleBinding object.
func ClusterRoleBindingGetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	clusterRoleBinding, success := obj.(*rbacv1.ClusterRoleBinding)
	if !success {
		return nil, nil, storage.ErrObjectIsNotAClusterRoleBinding
	}

	return labels.Set(clusterRoleBinding.Labels), ClusterRoleBindingSelectableFields(clusterRoleBinding), nil
}

// ClusterRoleBindingSelectableFields returns a field set that can be used for filter selection.
func ClusterRoleBindingSelectableFields(obj *rbacv1.ClusterRoleBinding) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// ObjectNameFuncClusterRoleBinding returns the name of the object.
func ObjectNameFuncClusterRoleBinding(obj runtime.Object) (string, error) {
	clusterRoleBinding, ok := obj.(*rbacv1.ClusterRoleBinding)
	if !ok {
		return "", storage.ErrObjectIsNotAClusterRoleBinding
	}

	return clusterRoleBinding.Name, nil
}

// clusterRoleBindingStrategy implements RESTCreateStrategy and RESTUpdateStrategy
// Heavily inspired by:
// https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/rbac/clusterrolebinding/strategy.go
type clusterRoleBindingStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = clusterRoleBindingStrategy{}
var _ rest.RESTUpdateStrategy = clusterRoleBindingStrategy{}
var _ rest.RESTDeleteStrategy = clusterRoleBindingStrategy{}
var _ rest.NamespaceScopedStrategy = clusterRoleBindingStrategy{}

// NamespaceScoped is false for ClusterRoleBindings.
func (clusterRoleBindingStrategy) NamespaceScoped() bool {
	return false
}

// AllowCreateOnUpdate is false for ClusterRoleBindings.
func (clusterRoleBindingStrategy) AllowCreateOnUpdate() bool {
	return false
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (clusterRoleBindingStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (clusterRoleBindingStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.ClusterRoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRoleBinding, obj))

		return
	}
}

// WarningsOnUpdate returns warnings for the update of the given object.
func (clusterRoleBindingStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (clusterRoleBindingStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.ClusterRoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRoleBinding, obj))

		return
	}

	_, success = old.(*rbacv1.ClusterRoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRoleBinding, old))

		return
	}
}

// Validate validates a new ClusterRoleBinding. Validation must check for a correct signature.
func (clusterRoleBindingStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	clusterRoleBindingObject, success := obj.(*rbacv1.ClusterRoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAClusterRoleBinding.Error())}
	}

	allErrs := validation.ValidateObjectMeta(
		&clusterRoleBindingObject.ObjectMeta,
		false,
		validateClusterRoleBindingName,
		field.NewPath("metadata"))

	if clusterRoleBindingObject.RoleRef.APIGroup != rbacv1.GroupName {
		allErrs = append(allErrs, field.NotSupported(
			field.NewPath("roleRef", "apiGroup"),
			clusterRoleBindingObject.RoleRef.APIGroup,
			[]string{rbac.GroupName}))
	}

	if len(clusterRoleBindingObject.RoleRef.Name) == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("roleRef", "name"),
			""))
	}

	return allErrs
}

// ValidateUpdate is the default update validation for an end user.
func (r clusterRoleBindingStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newClusterRoleBindingObject, success := obj.(*rbacv1.ClusterRoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAClusterRoleBinding.Error())}
	}

	oldClusterRoleBindingObject, success := old.(*rbacv1.ClusterRoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAClusterRoleBinding.Error())}
	}

	allErrs := r.Validate(ctx, newClusterRoleBindingObject)
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(
		&newClusterRoleBindingObject.ObjectMeta,
		&oldClusterRoleBindingObject.ObjectMeta,
		field.NewPath("metadata"))...)

	return allErrs
}

// Canonicalize normalizes the object after validation.
func (clusterRoleBindingStrategy) Canonicalize(_ runtime.Object) {}

// AllowUnconditionalUpdate is false for ClusterRoleBindings.
func (clusterRoleBindingStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func validateClusterRoleBindingName(name string, _ bool) []string {
	return k8spath.IsValidPathSegmentName(name)
}
