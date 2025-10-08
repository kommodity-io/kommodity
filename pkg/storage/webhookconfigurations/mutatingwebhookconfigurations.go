package webhookconfigurations

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/kine"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
)

var (
	mutatingGR   = schema.GroupResource{Group: admissionv1.GroupName, Resource: "mutatingwebhookconfigurations"}
	validatingGR = schema.GroupResource{Group: admissionv1.GroupName, Resource: "validatingwebhookconfigurations"}
)

// --- Strategy (cluster-scoped, minimal validation) ---

type webhookStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = webhookStrategy{}
var _ rest.RESTUpdateStrategy = webhookStrategy{}
var _ rest.RESTDeleteStrategy = webhookStrategy{}

func (webhookStrategy) NamespaceScoped() bool                                            { return false } // cluster-scoped
func (webhookStrategy) PrepareForCreate(context.Context, runtime.Object)                 {}
func (webhookStrategy) PrepareForUpdate(context.Context, runtime.Object, runtime.Object) {}
func (webhookStrategy) Validate(context.Context, runtime.Object) []error                 { return nil }
func (webhookStrategy) AllowCreateOnUpdate() bool                                        { return false }
func (webhookStrategy) ValidateUpdate(context.Context, runtime.Object, runtime.Object) []error {
	return nil
}
func (webhookStrategy) WarningsOnCreate(context.Context, runtime.Object) []string { return nil }
func (webhookStrategy) WarningsOnUpdate(context.Context, runtime.Object, runtime.Object) []string {
	return nil
}

// --- AttrFuncs & Predicates ---

func mutatingAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	o, ok := obj.(*admissionv1.MutatingWebhookConfiguration)
	if !ok {
		return nil, nil, fmt.Errorf("object is not MutatingWebhookConfiguration")
	}
	return labels.Set(o.GetLabels()), fields.Set{
		"metadata.name": o.GetName(),
	}, nil
}
func mutatingPredicate(labelSel labels.Selector, fieldSel fields.Selector) *generic.SelectionPredicate {
	return &generic.SelectionPredicate{
		Label:    labelSel,
		Field:    fieldSel,
		GetAttrs: mutatingAttrs,
	}
}

func validatingAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	o, ok := obj.(*admissionv1.ValidatingWebhookConfiguration)
	if !ok {
		return nil, nil, fmt.Errorf("object is not ValidatingWebhookConfiguration")
	}
	return labels.Set(o.GetLabels()), fields.Set{
		"metadata.name": o.GetName(),
	}, nil
}
func validatingPredicate(labelSel labels.Selector, fieldSel fields.Selector) *generic.SelectionPredicate {
	return &generic.SelectionPredicate{
		Label:    labelSel,
		Field:    fieldSel,
		GetAttrs: validatingAttrs,
	}
}

// --- REST implementations ---

type MutatingWebhookConfigurationREST struct{ *registry.Store }
type ValidatingWebhookConfigurationREST struct{ *registry.Store }

func NewMutatingWebhookConfigurationsREST(
	kineCfg kine.StorageConfig, // same type you pass to other storages in setupLegacyAPI
	scheme runtime.Scheme,
) (*MutatingWebhookConfigurationREST, error) {
	strategy := webhookStrategy{ObjectTyper: scheme, NameGenerator: names.SimpleNameGenerator}

	store := &registry.Store{
		NewFunc:                  func() runtime.Object { return &admissionv1.MutatingWebhookConfiguration{} },
		NewListFunc:              func() runtime.Object { return &admissionv1.MutatingWebhookConfigurationList{} },
		PredicateFunc:            mutatingPredicate,
		DefaultQualifiedResource: mutatingGR,
		CreateStrategy:           strategy,
		UpdateStrategy:           strategy,
		DeleteStrategy:           strategy,
		TableConvertor:           rest.NewDefaultTableConvertor(mutatingGR),
	}

	// The Storage options come from your Kine config (mirrors other storages you have)
	opts := &generic.StoreOptions{
		RESTOptions: kineCfg.RESTOptionsGetter, // provided by your kine package
		AttrFunc:    mutatingAttrs,
	}
	if err := store.CompleteWithOptions(opts); err != nil {
		return nil, err
	}
	return &MutatingWebhookConfigurationREST{Store: store}, nil
}

func NewValidatingWebhookConfigurationsREST(
	kineCfg kine.StorageConfig,
	scheme *runtime.Scheme,
) (*ValidatingWebhookConfigurationREST, error) {
	strategy := webhookStrategy{ObjectTyper: scheme, NameGenerator: names.SimpleNameGenerator}

	store := &registry.Store{
		NewFunc:                  func() runtime.Object { return &admissionv1.ValidatingWebhookConfiguration{} },
		NewListFunc:              func() runtime.Object { return &admissionv1.ValidatingWebhookConfigurationList{} },
		PredicateFunc:            validatingPredicate,
		DefaultQualifiedResource: validatingGR,
		CreateStrategy:           strategy,
		UpdateStrategy:           strategy,
		DeleteStrategy:           strategy,
		TableConvertor:           rest.NewDefaultTableConvertor(validatingGR),
	}
	opts := &generic.StoreOptions{
		RESTOptions: kineCfg.RESTOptionsGetter,
		AttrFunc:    validatingAttrs,
	}
	if err := store.CompleteWithOptions(opts); err != nil {
		return nil, err
	}
	return &ValidatingWebhookConfigurationREST{Store: store}, nil
}
