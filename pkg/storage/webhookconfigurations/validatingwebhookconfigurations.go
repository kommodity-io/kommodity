// Package webhookconfigurations implements the storage strategy towards kine for the admission registration resource.
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
	apistorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const validatingWebhookConfigurationResource = "validatingwebhookconfigurations"

// NewValidatingWebhookConfigurationREST creates a REST interface for validating webhook configurations.
func NewValidatingWebhookConfigurationREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(admissionregistrationv1.Resource(validatingWebhookConfigurationResource)),
		func() runtime.Object { return &admissionregistrationv1.ValidatingWebhookConfiguration{} },
		func() runtime.Object { return &admissionregistrationv1.ValidatingWebhookConfigurationList{} },
		"/"+validatingWebhookConfigurationResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	validatingWebhookConfigurationStrategy := validatingWebhookConfigurationStrategy{
		&scheme,
		names.SimpleNameGenerator,
	}

	return &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &admissionregistrationv1.ValidatingWebhookConfiguration{} },
		NewListFunc:   func() runtime.Object { return &admissionregistrationv1.ValidatingWebhookConfigurationList{} },
		PredicateFunc: VWCObjectPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + validatingWebhookConfigurationResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+validatingWebhookConfigurationResource, name), nil
		},
		ObjectNameFunc: VWCObjectNameFunc,
		CreateStrategy: validatingWebhookConfigurationStrategy,
		UpdateStrategy: validatingWebhookConfigurationStrategy,
		DeleteStrategy: validatingWebhookConfigurationStrategy,
		Storage:        dryRunnableStorage,
	}, nil
}

// ObjectNameFunc returns the name of the object.
func VWCObjectNameFunc(obj runtime.Object) (string, error) {
	vwc, ok := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !ok {
		return "", storage.ErrObjectIsNotAValidatingWebhookConfiguration
	}

	return vwc.Name, nil
}

// VWCObjectPredicateFunc returns a selection predicate for filtering ValidatingWebhookConfiguration objects.
func VWCObjectPredicateFunc(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: VWCGetAttrs,
	}
}

// VWCGetAttrs returns labels and fields for a ValidatingWebhookConfiguration object.
func VWCGetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	vwc, success := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		return nil, nil, storage.ErrObjectIsNotAValidatingWebhookConfiguration
	}

	return labels.Set(vwc.Labels), VWCSelectableFields(vwc), nil
}

// VWCSelectableFields returns a field set that can be used for filter selection.
func VWCSelectableFields(obj *admissionregistrationv1.ValidatingWebhookConfiguration) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// validatingWebhookConfigurationStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/admissionregistration/validatingwebhookconfiguration/strategy.go
type validatingWebhookConfigurationStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = validatingWebhookConfigurationStrategy{}
var _ rest.RESTUpdateStrategy = validatingWebhookConfigurationStrategy{}
var _ rest.RESTDeleteStrategy = validatingWebhookConfigurationStrategy{}
var _ rest.NamespaceScopedStrategy = validatingWebhookConfigurationStrategy{}

// NamespaceScoped returns false as ValidatingWebhookConfiguration is not namespaced.
func (validatingWebhookConfigurationStrategy) NamespaceScoped() bool {
	return false
}

// PrepareForCreate sets the generation number to 1 and clears the status.
func (validatingWebhookConfigurationStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	logger := logging.FromContext(ctx)

	vwc, success := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		logger.Error(storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error(), zap.Any("object", obj))

		return
	}

	vwc.Generation = 1
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (validatingWebhookConfigurationStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (validatingWebhookConfigurationStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newvwc, success := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		logging.FromContext(ctx).Error(storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error(), zap.Any("object", obj))

		return
	}

	oldvwc, success := old.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		logging.FromContext(ctx).Error(storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error(), zap.Any("object", old))

		return
	}

	// Any changes to the spec increment the generation number, any changes to the
	// status should reflect the generation number of the corresponding object.
	// See metav1.ObjectMeta description for more information on Generation.
	if !reflect.DeepEqual(oldvwc.Webhooks, newvwc.Webhooks) {
		newvwc.Generation = oldvwc.Generation + 1
	}
}

// Validate validates a new validatingWebhookConfiguration.
func (validatingWebhookConfigurationStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	vwc, success := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error())}
	}

	allErrors := apimachineryvalidation.ValidateObjectMeta(&vwc.ObjectMeta, false,
		apimachineryvalidation.NameIsDNSSubdomain, field.NewPath("metadata"))

	// hookNames := sets.NewString()
	// for i, hook := range vwc.Webhooks {
	// 	allErrors = append(allErrors, validateMutatingWebhook(&hook, opts, field.NewPath("webhooks").Index(i))...)
	// 	allErrors = append(allErrors, validateAdmissionReviewVersions(hook.AdmissionReviewVersions, true,
	// 		field.NewPath("webhooks").Index(i).Child("admissionReviewVersions"))...)

	// 	if len(hook.Name) > 0 {
	// 		if hookNames.Has(hook.Name) {
	// 			allErrors = append(allErrors, field.Duplicate(field.NewPath("webhooks").Index(i).Child("name"), hook.Name))
	// 		} else {
	// 			hookNames.Insert(hook.Name)
	// 		}
	// 	}
	// }
	return allErrors
}

// Canonicalize normalizes the object after validation.
func (validatingWebhookConfigurationStrategy) Canonicalize(obj runtime.Object) {
}

// AllowCreateOnUpdate is false for mutatingWebhookConfiguration; this means you may not create one with a PUT request.
func (validatingWebhookConfigurationStrategy) AllowCreateOnUpdate() bool {
	return false
}

// ValidateUpdate is the default update validation for an end user.
func (validatingWebhookConfigurationStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newvwc, success := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error())}
	}

	_, success = old.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAValidatingWebhookConfiguration.Error())}
	}

	errorList := apimachineryvalidation.ValidateObjectMeta(&newvwc.ObjectMeta, false,
		apimachineryvalidation.NameIsDNSSubdomain, field.NewPath("metadata"))

	return errorList
}

// WarningsOnUpdate returns warnings for the given update.
func (validatingWebhookConfigurationStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}

// AllowUnconditionalUpdate is the default update policy for validatingWebhookConfigurationStrategy objects. Status update should
// only be allowed if version match.
func (validatingWebhookConfigurationStrategy) AllowUnconditionalUpdate() bool {
	return false
}
