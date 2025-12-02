// Package rbac implements storage for RBAC resources.
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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/kubernetes/pkg/apis/rbac"
)

const roleBindingResource = "rolebindings"

// NewRoleBindingREST creates a REST interface for rbacv1 RoleBinding resource.
//
//nolint:dupl // Similar to pkg/storage/rbac/roles.go::NewRoleREST but not identical.
func NewRoleBindingREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(roleBindingResource)),
		func() runtime.Object { return &rbacv1.RoleBinding{} },
		func() runtime.Object { return &rbacv1.RoleBindingList{} },
		"/"+roleBindingResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	roleBindingStrategy := roleBindingStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &rbacv1.RoleBinding{} },
		NewListFunc:   func() runtime.Object { return &rbacv1.RoleBindingList{} },
		PredicateFunc: storage.NamespacedPredicateFunc(),
		KeyRootFunc:   func(_ context.Context) string { return "/" + roleBindingResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+roleBindingResource, name), nil
		},
		ObjectNameFunc: ObjectNameFuncRoleBinding,
		CreateStrategy: roleBindingStrategy,
		UpdateStrategy: roleBindingStrategy,
		DeleteStrategy: roleBindingStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ObjectNameFuncRoleBinding returns the name of the object.
func ObjectNameFuncRoleBinding(obj runtime.Object) (string, error) {
	roleBinding, ok := obj.(*rbacv1.RoleBinding)
	if !ok {
		return "", storage.ErrObjectIsNotARoleBinding
	}

	return roleBinding.Name, nil
}

// roleBindingStrategy implements RESTCreateStrategy and RESTUpdateStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/rbac/rolebinding/strategy.go
type roleBindingStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = roleBindingStrategy{}
var _ rest.RESTUpdateStrategy = roleBindingStrategy{}
var _ rest.NamespaceScopedStrategy = roleBindingStrategy{}

// NamespaceScoped is true for RoleBindings.
func (roleBindingStrategy) NamespaceScoped() bool {
	return true
}

// AllowCreateOnUpdate is true for RoleBindings.
func (roleBindingStrategy) AllowCreateOnUpdate() bool {
	return true
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (roleBindingStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (roleBindingStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.RoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARoleBinding, obj))

		return
	}
}

// WarningsOnUpdate returns warnings for the update of the given object.
func (roleBindingStrategy) WarningsOnUpdate(_ context.Context, _ runtime.Object, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (roleBindingStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.RoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARoleBinding, obj))

		return
	}

	_, success = old.(*rbacv1.RoleBinding)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARoleBinding, old))

		return
	}
}

// Validate validates a new RoleBinding. Validation must check for a correct signature.
func (roleBindingStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	roleBindingObject, success := obj.(*rbacv1.RoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotARoleBinding.Error())}
	}

	allErrs := validation.ValidateObjectMeta(
		&roleBindingObject.ObjectMeta,
		true,
		validateRoleName,
		field.NewPath("metadata"))

	if roleBindingObject.RoleRef.APIGroup != rbacv1.GroupName {
		allErrs = append(allErrs, field.NotSupported(
			field.NewPath("roleRef", "apiGroup"),
			roleBindingObject.RoleRef.APIGroup,
			[]string{rbac.GroupName}))
	}

	if len(roleBindingObject.RoleRef.Name) == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("roleRef", "name"),
			""))
	}

	return allErrs
}

// ValidateUpdate is the default update validation for an end user.
func (r roleBindingStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newRoleBindingObject, success := obj.(*rbacv1.RoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotARoleBinding.Error())}
	}

	oldRoleBindingObject, success := old.(*rbacv1.RoleBinding)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotARoleBinding.Error())}
	}

	allErrs := r.Validate(ctx, newRoleBindingObject)
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(
		&newRoleBindingObject.ObjectMeta,
		&oldRoleBindingObject.ObjectMeta,
		field.NewPath("metadata"))...)

	return allErrs
}

// Canonicalize normalizes the object after validation.
func (roleBindingStrategy) Canonicalize(_ runtime.Object) {}

// AllowUnconditionalUpdate is true for Roles.
func (roleBindingStrategy) AllowUnconditionalUpdate() bool {
	return true
}
