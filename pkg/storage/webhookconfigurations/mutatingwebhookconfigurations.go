// Package webhookconfigurations implements the storage strategy towards kine for the admission registration resource.
//
//nolint:dupl,lll // This file is very similar to validatingwebhookconfigurations.go but the differences are still significant.
package webhookconfigurations

import (
	"context"
	"fmt"
	"path"
	"reflect"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/storage"
	"go.uber.org/zap"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apimachineryvalidation "k8s.io/apimachinery/pkg/api/validation"
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

const mutatingWebhookConfigurationResource = "mutatingwebhookconfigurations"

// NewMutatingWebhookConfigurationREST creates a REST interface for mutating webhook configurations.
func NewMutatingWebhookConfigurationREST(storageConfig storagebackend.Config,
	scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(admissionregistrationv1.Resource(mutatingWebhookConfigurationResource)),
		func() runtime.Object { return &admissionregistrationv1.MutatingWebhookConfiguration{} },
		func() runtime.Object { return &admissionregistrationv1.MutatingWebhookConfigurationList{} },
		"/"+mutatingWebhookConfigurationResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	mutatingWebhookConfigurationStrategy := mutatingWebhookConfigurationStrategy{
		ObjectTyper:   &scheme,
		NameGenerator: names.SimpleNameGenerator,
	}

	return &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &admissionregistrationv1.MutatingWebhookConfiguration{} },
		NewListFunc:   func() runtime.Object { return &admissionregistrationv1.MutatingWebhookConfigurationList{} },
		PredicateFunc: storage.PredicateFunc(MWCGetAttrs),
		KeyRootFunc:   func(_ context.Context) string { return "/" + mutatingWebhookConfigurationResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+mutatingWebhookConfigurationResource, name), nil
		},
		ObjectNameFunc: MWCObjectNameFunc,
		CreateStrategy: mutatingWebhookConfigurationStrategy,
		UpdateStrategy: mutatingWebhookConfigurationStrategy,
		DeleteStrategy: mutatingWebhookConfigurationStrategy,
		Storage:        dryRunnableStorage,
	}, nil
}

// MWCObjectNameFunc returns the name of the object.
func MWCObjectNameFunc(obj runtime.Object) (string, error) {
	mwc, ok := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !ok {
		return "", storage.ErrObjectIsNotAnMutatingWebhookConfiguration
	}

	return mwc.Name, nil
}

// MWCGetAttrs returns labels and fields for a MutatingWebhookConfiguration object.
func MWCGetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	mwc, success := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		return nil, nil, storage.ErrObjectIsNotAnMutatingWebhookConfiguration
	}

	return labels.Set(mwc.Labels), MWCSelectableFields(mwc), nil
}

// MWCSelectableFields returns a field set that can be used for filter selection.
func MWCSelectableFields(obj *admissionregistrationv1.MutatingWebhookConfiguration) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// mutatingWebhookConfigurationStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/admissionregistration/mutatingwebhookconfiguration/strategy.go
//
//nolint:lll
type mutatingWebhookConfigurationStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = mutatingWebhookConfigurationStrategy{}
var _ rest.RESTUpdateStrategy = mutatingWebhookConfigurationStrategy{}
var _ rest.RESTDeleteStrategy = mutatingWebhookConfigurationStrategy{}
var _ rest.NamespaceScopedStrategy = mutatingWebhookConfigurationStrategy{}

// var _ rest.Watcher = mutatingWebhookConfigurationStrategy{}
// var _ rest.Lister = mutatingWebhookConfigurationStrategy{}

// NamespaceScoped returns false as MutatingWebhookConfiguration is not namespaced.
func (mutatingWebhookConfigurationStrategy) NamespaceScoped() bool {
	return false
}

// PrepareForCreate sets the generation number to 1 and clears the status.
func (mutatingWebhookConfigurationStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	mwc, success := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		logger.Error(storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error(), zap.Any("object", obj))

		return
	}

	mwc.Generation = 1
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (mutatingWebhookConfigurationStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (mutatingWebhookConfigurationStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newMWC, success := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		logging.FromContext(ctx).Error(storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error(), zap.Any("object", obj))

		return
	}

	oldMWC, success := old.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		logging.FromContext(ctx).Error(storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error(), zap.Any("object", old))

		return
	}

	// Any changes to the spec increment the generation number, any changes to the
	// status should reflect the generation number of the corresponding object.
	// See metav1.ObjectMeta description for more information on Generation.
	if !reflect.DeepEqual(oldMWC.Webhooks, newMWC.Webhooks) {
		newMWC.Generation = oldMWC.Generation + 1
	}
}

// Validate validates a new mutatingWebhookConfiguration.
func (mutatingWebhookConfigurationStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	mwc, success := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error())}
	}

	errorList := apimachineryvalidation.ValidateObjectMeta(&mwc.ObjectMeta, false,
		apimachineryvalidation.NameIsDNSSubdomain, field.NewPath("metadata"))

	return errorList
}

// Canonicalize normalizes the object after validation.
func (mutatingWebhookConfigurationStrategy) Canonicalize(_ runtime.Object) {
}

// AllowCreateOnUpdate is false for mutatingWebhookConfiguration; this means you may not create one with a PUT request.
func (mutatingWebhookConfigurationStrategy) AllowCreateOnUpdate() bool {
	return false
}

// ValidateUpdate is the default update validation for an end user.
func (mutatingWebhookConfigurationStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newMWC, success := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error())}
	}

	_, success = old.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAnMutatingWebhookConfiguration.Error())}
	}

	errorList := apimachineryvalidation.ValidateObjectMeta(&newMWC.ObjectMeta, false,
		apimachineryvalidation.NameIsDNSSubdomain, field.NewPath("metadata"))

	return errorList
}

// WarningsOnUpdate returns warnings for the given update.
func (mutatingWebhookConfigurationStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// AllowUnconditionalUpdate is the default update policy for mutatingWebhookConfiguration objects. Status update should
// only be allowed if version match.
func (mutatingWebhookConfigurationStrategy) AllowUnconditionalUpdate() bool {
	return false
}
