// Package events implements the storage strategy towards kine for the core v1 Event resource.
package events

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1beta1 "k8s.io/api/events/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	storageerr "github.com/kommodity-io/kommodity/pkg/storage"

	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/event/strategy.go

const (
	eventResource                = "events"
	reportingInstanceLengthLimit = 128
	actionLengthLimit            = 128
	reasonLengthLimit            = 128
	noteLengthLimit              = 1024
	minEventSeriesCount          = 2
)

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"ev"}
}

// NewEventsREST creates a REST interface for corev1 Event resource.
func NewEventsREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(eventResource)),
		func() runtime.Object { return &corev1.Event{} },
		func() runtime.Object { return &corev1.EventList{} },
		"/"+eventResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	eventStrategy := eventStrategy{
		scheme: scheme,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.Event{} },
		NewListFunc:   func() runtime.Object { return &corev1.EventList{} },
		PredicateFunc: EventPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + eventResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+eventResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: eventStrategy,
		UpdateStrategy: eventStrategy,
		DeleteStrategy: eventStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// EventPredicateFunc returns a selection predicate for filtering Event objects.
func EventPredicateFunc(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a Event object.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return nil, nil, storageerr.ErrObjectIsNotAnEvent
	}

	objectMetaFieldsSet := generic.ObjectMetaFieldsSet(&event.ObjectMeta, true)

	source := event.Source.Component
	if source == "" {
		source = event.ReportingController
	}

	specificFieldsSet := fields.Set{
		"involvedObject.kind":            event.InvolvedObject.Kind,
		"involvedObject.namespace":       event.InvolvedObject.Namespace,
		"involvedObject.name":            event.InvolvedObject.Name,
		"involvedObject.uid":             string(event.InvolvedObject.UID),
		"involvedObject.apiVersion":      event.InvolvedObject.APIVersion,
		"involvedObject.resourceVersion": event.InvolvedObject.ResourceVersion,
		"involvedObject.fieldPath":       event.InvolvedObject.FieldPath,
		"reason":                         event.Reason,
		"reportingComponent":             event.ReportingController, // use the core/v1 field name
		"source":                         source,
		"type":                           event.Type,
	}

	fields := generic.MergeFieldsSets(specificFieldsSet, objectMetaFieldsSet)

	return labels.Set(event.Labels), fields, nil
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return "", storageerr.ErrObjectIsNotAnEvent
	}

	return event.Name, nil
}

// eventStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy.
type eventStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = eventStrategy{}
var _ rest.RESTUpdateStrategy = eventStrategy{}
var _ rest.RESTDeleteStrategy = eventStrategy{}
var _ rest.NamespaceScopedStrategy = eventStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (eventStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate sets defaults for new objects.
func (eventStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {}

// WarningsOnCreate returns warnings for create operations.
func (eventStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

// PrepareForUpdate sets defaults for updated objects.
func (eventStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {}

// WarningsOnUpdate returns warnings for update operations.
func (eventStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForDelete clears fields before deletion.
func (eventStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (eventStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	groupVersion := requestGroupVersion(ctx)

	event, ok := obj.(*corev1.Event)
	if !ok {
		return field.ErrorList{field.Invalid(field.NewPath("object"), obj, "object is not a *corev1.Event")}
	}

	return validateEventCreate(event, groupVersion)
}

// ValidateUpdate validates updated objects.
func (eventStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	groupVersion := requestGroupVersion(ctx)

	event, success := obj.(*corev1.Event)
	if !success {
		return field.ErrorList{field.Invalid(field.NewPath("object"), obj, "object is not a *corev1.Event")}
	}

	oldEvent, success := old.(*corev1.Event)
	if !success {
		return field.ErrorList{field.Invalid(field.NewPath("old"), old, "object is not a *corev1.Event")}
	}

	return validateEventUpdate(event, oldEvent, groupVersion)
}

// Canonicalize normalizes objects.
func (eventStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (eventStrategy) AllowCreateOnUpdate() bool {
	return true
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (eventStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// GenerateName generates a name using the given base string.
func (eventStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object.
func (es eventStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := es.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (es eventStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return es.scheme.Recognizes(gvk)
}

// requestGroupVersion returns the group/version associated with the given context, or a zero-value group/version.
func requestGroupVersion(ctx context.Context) schema.GroupVersion {
	if requestInfo, found := genericapirequest.RequestInfoFrom(ctx); found {
		return schema.GroupVersion{Group: requestInfo.APIGroup, Version: requestInfo.APIVersion}
	}

	return schema.GroupVersion{}
}

// legacyValidateEvent makes sure that the event makes sense.
//nolint:cyclop, funlen // Too long or too complex due to many error checks, no real complexity here
func legacyValidateEvent(event *corev1.Event, requestVersion schema.GroupVersion) field.ErrorList {
	allErrs := field.ErrorList{}
	// Because go
	zeroTime := time.Time{}

	reportingControllerFieldName := "reportingController"
	if requestVersion == corev1.SchemeGroupVersion {
		reportingControllerFieldName = "reportingComponent"
	}

	// "New" Events need to have EventTime set, so it's validating old object.
	//nolint:nestif // Nested ifs are used here for clarity
	if event.EventTime.Time.Equal(zeroTime) {
		// Make sure event.Namespace and the involvedInvolvedObject.Namespace agree
		if len(event.InvolvedObject.Namespace) == 0 {
			// event.Namespace must also be empty (or "default", for compatibility with old clients)
			if event.Namespace != metav1.NamespaceNone && event.Namespace != metav1.NamespaceDefault {
				allErrs = append(allErrs, field.Invalid(field.NewPath("involvedObject", "namespace"),
					event.InvolvedObject.Namespace,
					"does not match event.namespace"),
				)
			}
		} else {
			// event namespace must match
			if event.Namespace != event.InvolvedObject.Namespace {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("involvedObject", "namespace"),
					event.InvolvedObject.Namespace,
					"does not match event.namespace",
				))
			}
		}
	} else {
		if len(event.InvolvedObject.Namespace) == 0 &&
			event.Namespace != metav1.NamespaceDefault &&
			event.Namespace != metav1.NamespaceSystem {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("involvedObject", "namespace"),
				event.InvolvedObject.Namespace,
				"does not match event.namespace",
			),
			)
		}

		if len(event.ReportingController) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath(reportingControllerFieldName), ""))
		}

		qualifiedNameErrs := validateQualifiedName(
			event.ReportingController,
			field.NewPath(reportingControllerFieldName),
		)

		allErrs = append(allErrs, qualifiedNameErrs...)
		if len(event.ReportingInstance) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath("reportingInstance"), ""))
		}

		if len(event.ReportingInstance) > reportingInstanceLengthLimit {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("reportingInstance"),
				"",
				fmt.Sprintf("can have at most %v characters", reportingInstanceLengthLimit),
			))
		}

		if len(event.Action) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath("action"), ""))
		}

		if len(event.Action) > actionLengthLimit {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("action"),
				"",
				fmt.Sprintf("can have at most %v characters", actionLengthLimit),
			),
			)
		}

		if len(event.Reason) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath("reason"), ""))
		}

		if len(event.Reason) > reasonLengthLimit {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("reason"),
				"",
				fmt.Sprintf("can have at most %v characters", reasonLengthLimit),
			),
			)
		}

		if len(event.Message) > noteLengthLimit {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("message"),
				"",
				fmt.Sprintf("can have at most %v characters", noteLengthLimit),
			))
		}
	}

	for _, msg := range utilvalidation.IsDNS1123Subdomain(event.Namespace) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("namespace"), event.Namespace, msg))
	}

	return allErrs
}

// ValidateQualifiedName validates if name is what Kubernetes calls a "qualified name".
func validateQualifiedName(value string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for _, msg := range utilvalidation.IsQualifiedName(value) {
		allErrs = append(allErrs, field.Invalid(fldPath, value, msg))
	}

	return allErrs
}

//nolint:cyclop // Too long or too complex due to many error checks, no real complexity here
func validateEventCreate(event *corev1.Event, requestVersion schema.GroupVersion) field.ErrorList {
	// Make sure events always pass legacy validation.
	allErrs := legacyValidateEvent(event, requestVersion)
	if requestVersion == corev1.SchemeGroupVersion || requestVersion == eventsv1beta1.SchemeGroupVersion {
		// No further validation for backwards compatibility.
		return allErrs
	}

	// Strict validation applies to creation via events.k8s.io/v1 API and newer.
	allErrs = append(allErrs, validation.ValidateObjectMeta(
		&event.ObjectMeta,
		true,
		validation.NameIsDNSSubdomain,
		field.NewPath("metadata"))...,
	)
	allErrs = append(allErrs, validateV1EventSeries(event)...)

	zeroTime := time.Time{}
	if event.EventTime.Time.Equal(zeroTime) {
		allErrs = append(allErrs, field.Required(field.NewPath("eventTime"), ""))
	}

	if event.Type != corev1.EventTypeNormal && event.Type != corev1.EventTypeWarning {
		allErrs = append(allErrs, field.Invalid(field.NewPath("type"), "", fmt.Sprintf("has invalid value: %v", event.Type)))
	}

	if event.FirstTimestamp.Time != zeroTime {
		allErrs = append(allErrs, field.Invalid(field.NewPath("firstTimestamp"), "", "needs to be unset"))
	}

	if event.LastTimestamp.Time != zeroTime {
		allErrs = append(allErrs, field.Invalid(field.NewPath("lastTimestamp"), "", "needs to be unset"))
	}

	if event.Count != 0 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("count"), "", "needs to be unset"))
	}

	if event.Source.Component != "" || event.Source.Host != "" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("source"), "", "needs to be unset"))
	}

	return allErrs
}

//nolint:funlen // Too long or too complex due to many error checks, no real complexity here
func validateEventUpdate(newEvent, oldEvent *corev1.Event, requestVersion schema.GroupVersion) field.ErrorList {
	// Make sure the new event always passes legacy validation.
	allErrs := legacyValidateEvent(newEvent, requestVersion)
	if requestVersion == corev1.SchemeGroupVersion || requestVersion == eventsv1beta1.SchemeGroupVersion {
		// No further validation for backwards compatibility.
		return allErrs
	}

	// Strict validation applies to update via events.k8s.io/v1 API and newer.
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(
		&newEvent.ObjectMeta,
		&oldEvent.ObjectMeta,
		field.NewPath("metadata"))...,
	)
	// if the series was modified, validate the new data
	if !reflect.DeepEqual(newEvent.Series, oldEvent.Series) {
		allErrs = append(allErrs, validateV1EventSeries(newEvent)...)
	}

	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.InvolvedObject,
		oldEvent.InvolvedObject,
		field.NewPath("involvedObject"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Reason,
		oldEvent.Reason,
		field.NewPath("reason"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Message,
		oldEvent.Message,
		field.NewPath("message"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Source,
		oldEvent.Source,
		field.NewPath("source"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.FirstTimestamp,
		oldEvent.FirstTimestamp,
		field.NewPath("firstTimestamp"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.LastTimestamp,
		oldEvent.LastTimestamp,
		field.NewPath("lastTimestamp"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(newEvent.Count, oldEvent.Count, field.NewPath("count"))...)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Reason,
		oldEvent.Reason,
		field.NewPath("reason"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(newEvent.Type, oldEvent.Type, field.NewPath("type"))...)

	// Disallow changes to eventTime greater than microsecond-level precision.
	// Tolerating sub-microsecond changes is required to tolerate updates
	// from clients that correctly truncate to microsecond-precision when serializing,
	// or from clients built with incorrect nanosecond-precision protobuf serialization.
	// See https://github.com/kubernetes/kubernetes/issues/111928
	newTruncated := newEvent.EventTime.Truncate(time.Microsecond).UTC()

	oldTruncated := oldEvent.EventTime.Truncate(time.Microsecond).UTC()
	if newTruncated != oldTruncated {
		allErrs = append(allErrs, validation.ValidateImmutableField(
			newEvent.EventTime,
			oldEvent.EventTime,
			field.NewPath("eventTime"))...,
		)
	}

	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Action,
		oldEvent.Action,
		field.NewPath("action"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.Related,
		oldEvent.Related,
		field.NewPath("related"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.ReportingController,
		oldEvent.ReportingController,
		field.NewPath("reportingController"))...,
	)
	allErrs = append(allErrs, validation.ValidateImmutableField(
		newEvent.ReportingInstance,
		oldEvent.ReportingInstance,
		field.NewPath("reportingInstance"))...,
	)

	return allErrs
}

func validateV1EventSeries(event *corev1.Event) field.ErrorList {
	allErrs := field.ErrorList{}
	zeroTime := time.Time{}

	if event.Series != nil {
		if event.Series.Count < minEventSeriesCount {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("series.count"),
				"",
				fmt.Sprintf("should be at least %d", minEventSeriesCount)),
			)
		}

		if event.Series.LastObservedTime.Time.Equal(zeroTime) {
			allErrs = append(allErrs, field.Required(field.NewPath("series.lastObservedTime"), ""))
		}
	}

	return allErrs
}
