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
)

const clusterRoleResource = "clusterroles"

// NewClusterRoleREST creates a REST interface for rbacv1 ClusterRole resource.
//
//nolint:dupl // Similar to pkg/storage/rbac/clusterrolebindings.go::NewClusterRoleBindingREST but not identical.
func NewClusterRoleREST(
	storageConfig storagebackend.Config,
	scheme runtime.Scheme,
) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(clusterRoleResource)),
		func() runtime.Object { return &rbacv1.ClusterRole{} },
		func() runtime.Object { return &rbacv1.ClusterRoleList{} },
		"/"+clusterRoleResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	clusterRoleStrategy := clusterRoleStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &rbacv1.ClusterRole{} },
		NewListFunc:   func() runtime.Object { return &rbacv1.ClusterRoleList{} },
		PredicateFunc: storage.PredicateFunc(ClusterRoleGetAttrs),
		KeyRootFunc:   func(_ context.Context) string { return "/" + clusterRoleResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+clusterRoleResource, name), nil
		},
		ObjectNameFunc: ObjectNameFuncClusterRole,
		CreateStrategy: clusterRoleStrategy,
		UpdateStrategy: clusterRoleStrategy,
		DeleteStrategy: clusterRoleStrategy,
		Storage:        dryRunnableStorage,
	}

	return restStore, nil
}

// ClusterRoleGetAttrs returns labels and fields for a ClusterRole object.
func ClusterRoleGetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	clusterRole, success := obj.(*rbacv1.ClusterRole)
	if !success {
		return nil, nil, storage.ErrObjectIsNotAClusterRole
	}

	return labels.Set(clusterRole.Labels), ClusterRoleSelectableFields(clusterRole), nil
}

// ClusterRoleSelectableFields returns a field set that can be used for filter selection.
func ClusterRoleSelectableFields(obj *rbacv1.ClusterRole) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// ObjectNameFuncClusterRole returns the name of the object.
func ObjectNameFuncClusterRole(obj runtime.Object) (string, error) {
	clusterRole, ok := obj.(*rbacv1.ClusterRole)
	if !ok {
		return "", storage.ErrObjectIsNotAClusterRole
	}

	return clusterRole.Name, nil
}

// clusterRoleStrategy implements RESTCreateStrategy and RESTUpdateStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/rbac/clusterrole/strategy.go
type clusterRoleStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = clusterRoleStrategy{}
var _ rest.RESTUpdateStrategy = clusterRoleStrategy{}
var _ rest.RESTDeleteStrategy = clusterRoleStrategy{}
var _ rest.NamespaceScopedStrategy = clusterRoleStrategy{}

// NamespaceScoped is false for ClusterRoles.
func (clusterRoleStrategy) NamespaceScoped() bool {
	return false
}

// AllowCreateOnUpdate is false for ClusterRoles.
func (clusterRoleStrategy) AllowCreateOnUpdate() bool {
	return false
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (clusterRoleStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (clusterRoleStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.ClusterRole)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRole, obj))

		return
	}
}

// WarningsOnUpdate returns warnings for the update of the given object.
func (clusterRoleStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (clusterRoleStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	logger := logging.FromContext(ctx)

	_, success := obj.(*rbacv1.ClusterRole)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRole, obj))

		return
	}

	_, success = old.(*rbacv1.ClusterRole)
	if !success {
		logger.Error(storage.ExpectedGot(storage.ErrObjectIsNotAClusterRole, obj))

		return
	}
}

// Validate validates a new ClusterRole. Validation must check for a correct signature.
func (clusterRoleStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	clusterRoleObject, success := obj.(*rbacv1.ClusterRole)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAClusterRole.Error())}
	}

	allErrs := validation.ValidateObjectMeta(
		&clusterRoleObject.ObjectMeta,
		false,
		validateClusterRoleName,
		field.NewPath("metadata"))

	for i, rule := range clusterRoleObject.Rules {
		errs := validatePolicyRule(rule, false, field.NewPath("rules").Index(i))
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
func (r clusterRoleStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newClusterRoleObject, success := obj.(*rbacv1.ClusterRole)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAClusterRole.Error())}
	}

	oldClusterRoleObject, success := old.(*rbacv1.ClusterRole)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAClusterRole.Error())}
	}

	allErrs := r.Validate(ctx, newClusterRoleObject)
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(
		&newClusterRoleObject.ObjectMeta,
		&oldClusterRoleObject.ObjectMeta,
		field.NewPath("metadata"))...)

	return allErrs
}

// Canonicalize normalizes the object after validation.
func (clusterRoleStrategy) Canonicalize(_ runtime.Object) {}

// AllowUnconditionalUpdate is false for ClusterRoles.
func (clusterRoleStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func validateClusterRoleName(name string, _ bool) []string {
	return k8spath.IsValidPathSegmentName(name)
}
