// Package events implements the storage strategy towards kine for the core v1 Event resource.
package events

import (
	"context"
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"

	storageerr "github.com/kommodity-io/kommodity/pkg/storage"

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

const eventResource = "events"

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

// eventStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/event/strategy.go
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
func (eventStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {}

// WarningsOnUpdate returns warnings for update operations.
func (eventStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// PrepareForDelete clears fields before deletion.
func (eventStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (eventStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {

}

// ValidateUpdate validates updated objects.
func (eventStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	groupVersion := requestGroupVersion(ctx)
	event := obj.(*corev1.Event)
	oldEvent := old.(*corev1.Event)
	return validation.ValidateEventUpdate(event, oldEvent, groupVersion)
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
func (ns eventStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := ns.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (ns eventStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return ns.scheme.Recognizes(gvk)
}
