// Package storage implements storage for Storage resources.
package storage

import (
	"context"
	"fmt"
	"path"

	"github.com/kommodity-io/kommodity/pkg/storage"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"sigs.k8s.io/structured-merge-diff/v4/fieldpath"
)

const volumeAttachmentResource = "volumeattachments"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

// volumeAttachmentStrategy implements RESTCreateStrategy and RESTUpdateStrategy
// Heavily inspired by:
// https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/storage/volumeattachment/strategy.go
type volumeAttachmentStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

// Strategy is the default logic that applies when creating and updating
// VolumeAttachment objects via the REST API.
//nolint:gochecknoglobals // Strategy is intended to be global.
var Strategy = volumeAttachmentStrategy{legacyscheme.Scheme, names.SimpleNameGenerator}

// NewVolumeAttachmentREST creates a REST interface for storagev1 VolumeAttachment resource.
func NewVolumeAttachmentREST(storageConfig storagebackend.Config, _ runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(volumeAttachmentResource)),
		func() runtime.Object { return &storagev1.VolumeAttachment{} },
		func() runtime.Object { return &storagev1.VolumeAttachmentList{} },
		"/"+volumeAttachmentResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	volumeAttachmentStrategy := Strategy

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &storagev1.VolumeAttachment{} },
		NewListFunc:   func() runtime.Object { return &storagev1.VolumeAttachmentList{} },
		PredicateFunc: storage.NamespacedPredicateFunc(),
		KeyRootFunc:   func(_ context.Context) string { return "/" + volumeAttachmentResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+volumeAttachmentResource, name), nil
		},
		ObjectNameFunc: ObjectNameFuncVolumeAttachment,
		CreateStrategy: volumeAttachmentStrategy,
		UpdateStrategy: volumeAttachmentStrategy,
		DeleteStrategy: volumeAttachmentStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// ObjectNameFuncVolumeAttachment returns the name of the object.
func ObjectNameFuncVolumeAttachment(obj runtime.Object) (string, error) {
	volumeAttachment, ok := obj.(*storagev1.VolumeAttachment)
	if !ok {
		return "", storage.ErrObjectIsNotAVolumeAttachment
	}

	return volumeAttachment.Name, nil
}

var _ rest.RESTCreateStrategy = volumeAttachmentStrategy{}
var _ rest.RESTUpdateStrategy = volumeAttachmentStrategy{}
var _ rest.NamespaceScopedStrategy = volumeAttachmentStrategy{}

func (volumeAttachmentStrategy) NamespaceScoped() bool {
	return false
}

// GetResetFields returns the set of fields that get reset by the strategy
// and should not be modified by the user.
func (volumeAttachmentStrategy) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	fields := map[fieldpath.APIVersion]*fieldpath.Set{
		"storage.k8s.io/v1": fieldpath.NewSet(
			fieldpath.MakePathOrDie("status"),
		),
	}

	return fields
}

func (volumeAttachmentStrategy) AllowCreateOnUpdate() bool {
	return false
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (volumeAttachmentStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (volumeAttachmentStrategy) PrepareForCreate(_ context.Context, obj runtime.Object) {
	volumeAttachment, ok := obj.(*storagev1.VolumeAttachment)
	if !ok {
		return
	}

	volumeAttachment.Status = storagev1.VolumeAttachmentStatus{}
}

// WarningsOnUpdate returns warnings for the given update.
func (volumeAttachmentStrategy) WarningsOnUpdate(_ context.Context, _ runtime.Object, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate sets the Status fields which is not allowed to be set by an end user updating a VolumeAttachment.
func (volumeAttachmentStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newVolumeAttachment, okNew := obj.(*storagev1.VolumeAttachment)

	oldVolumeAttachment, okOld := old.(*storagev1.VolumeAttachment)
	if !okNew || !okOld {
		return
	}

	newVolumeAttachment.Status = oldVolumeAttachment.Status
	// No need to increment Generation because we don't allow updates to spec
}

func (volumeAttachmentStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	obj, ok := obj.(*storagev1.VolumeAttachment)
	if !ok {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAVolumeAttachment.Error())}
	}

	return nil
}

func (v volumeAttachmentStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newVolumeAttachmentObj, okNew := obj.(*storagev1.VolumeAttachment)

	oldVolumeAttachmentObj, okOld := old.(*storagev1.VolumeAttachment)
	if !okNew || !okOld {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAVolumeAttachment.Error())}
	}

	err := v.Validate(ctx, newVolumeAttachmentObj)
	if err != nil {
		return err
	}

	err = v.Validate(ctx, oldVolumeAttachmentObj)
	if err != nil {
		return err
	}

	return nil
}

// Canonicalize normalizes the object after validation.
func (volumeAttachmentStrategy) Canonicalize(_ runtime.Object) {
}

func (volumeAttachmentStrategy) AllowUnconditionalUpdate() bool {
	return false
}

// volumeAttachmentStatusStrategy implements behavior for VolumeAttachmentStatus subresource.
type volumeAttachmentStatusStrategy struct {
	volumeAttachmentStrategy
}

// StatusStrategy is the default logic that applies when creating and updating
// VolumeAttachmentStatus subresource via the REST API.
//nolint:gochecknoglobals // Strategy is intended to be global.
var StatusStrategy = volumeAttachmentStatusStrategy{Strategy}

// GetResetFields returns the set of fields that get reset by the strategy
// and should not be modified by the user.
func (volumeAttachmentStatusStrategy) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	fields := map[fieldpath.APIVersion]*fieldpath.Set{
		"storage.k8s.io/v1": fieldpath.NewSet(
			fieldpath.MakePathOrDie("metadata"),
			fieldpath.MakePathOrDie("spec"),
		),
	}

	return fields
}

// PrepareForUpdate sets the Status fields which is not allowed to be set by an end user updating a VolumeAttachment.
func (volumeAttachmentStatusStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newVolumeAttachment, okNew := obj.(*storagev1.VolumeAttachment)
	oldVolumeAttachment, okOld := old.(*storagev1.VolumeAttachment)

	if !okNew || !okOld {
		return
	}

	newVolumeAttachment.Spec = oldVolumeAttachment.Spec
	metav1.ResetObjectMetaForStatus(&newVolumeAttachment.ObjectMeta, &oldVolumeAttachment.ObjectMeta)
}
