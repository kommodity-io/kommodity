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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const roleResource = "roles"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

// NewRoleREST creates a REST interface for rbacv1 Role resource.
//
//nolint:dupl // Similar to pkg/storage/rbac/rolebindings.go::NewRoleBindingREST but not identical.
func NewRoleREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(roleResource)),
		func() runtime.Object { return &rbacv1.Role{} },
		func() runtime.Object { return &rbacv1.RoleList{} },
		"/"+roleResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	roleStrategy := roleStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &rbacv1.Role{} },
		NewListFunc:   func() runtime.Object { return &rbacv1.RoleList{} },
		PredicateFunc: storage.NamespacedPredicateFunc(),
		KeyRootFunc:   func(_ context.Context) string { return "/" + roleResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+roleResource, name), nil
		},
		ObjectNameFunc: ObjectNameFuncRole,
		CreateStrategy: roleStrategy,
		UpdateStrategy: roleStrategy,
		DeleteStrategy: roleStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ObjectNameFuncRole returns the name of the object.
func ObjectNameFuncRole(obj runtime.Object) (string, error) {
	role, ok := obj.(*rbacv1.Role)
	if !ok {
		return "", storage.ErrObjectIsNotARole
	}

	return role.Name, nil
}

// roleStrategy implements RESTCreateStrategy and RESTUpdateStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/rbac/role/strategy.go
type roleStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = roleStrategy{}
var _ rest.RESTUpdateStrategy = roleStrategy{}
var _ rest.NamespaceScopedStrategy = roleStrategy{}

// NamespaceScoped is true for Roles.
func (roleStrategy) NamespaceScoped() bool {
	return true
}

// AllowCreateOnUpdate is true for Roles.
func (roleStrategy) AllowCreateOnUpdate() bool {
	return true
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (roleStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (roleStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.Role)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARole, obj))

		return
	}
}

// WarningsOnUpdate returns warnings for the update of the given object.
func (roleStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (roleStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.Role)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARole, obj))

		return
	}

	_, success = old.(*rbacv1.Role)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotARole, obj))

		return
	}
}

// Validate validates a new Role. Validation must check for a correct signature.
func (roleStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	roleObject, success := obj.(*rbacv1.Role)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotARole.Error())}
	}

	allErrs := validation.ValidateObjectMeta(
		&roleObject.ObjectMeta,
		true,
		validateRoleName,
		field.NewPath("metadata"))

	for i, rule := range roleObject.Rules {
		errs := validatePolicyRule(rule, true, field.NewPath("rules").Index(i))
		if len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	if len(allErrs) > 0 {
		return allErrs
	}

	return nil
}

// ValidateUpdate is the default update validation for an end user.
func (r roleStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newRoleObject, success := obj.(*rbacv1.Role)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotARole.Error())}
	}

	oldRoleObject, success := old.(*rbacv1.Role)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotARole.Error())}
	}

	allErrs := r.Validate(ctx, newRoleObject)
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(
		&newRoleObject.ObjectMeta,
		&oldRoleObject.ObjectMeta,
		field.NewPath("metadata"))...)

	return allErrs
}

// Canonicalize normalizes the object after validation.
func (roleStrategy) Canonicalize(_ runtime.Object) {}

// AllowUnconditionalUpdate is true for Roles.
func (roleStrategy) AllowUnconditionalUpdate() bool {
	return true
}

func validateRoleName(name string, _ bool) []string {
	return k8spath.IsValidPathSegmentName(name)
}

func validatePolicyRule(rule rbacv1.PolicyRule, isNamespaced bool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(rule.Verbs) == 0 {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("verbs"),
			"verbs must contain at least one value"))
	}

	if len(rule.NonResourceURLs) > 0 {
		if isNamespaced {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("nonResourceURLs"),
				rule.NonResourceURLs,
				"namespaced rules cannot apply to non-resource URLs"))
		}

		if len(rule.APIGroups) > 0 || len(rule.Resources) > 0 || len(rule.ResourceNames) > 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child(
				"nonResourceURLs"),
				rule.NonResourceURLs,
				"rules cannot apply to both regular resources and non-resource URLs"))
		}

		return allErrs
	}

	if len(rule.APIGroups) == 0 {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("apiGroups"),
			"resource rules must supply at least one api group"))
	}

	if len(rule.Resources) == 0 {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("resources"),
			"resource rules must supply at least one resource"))
	}

	return allErrs
}
