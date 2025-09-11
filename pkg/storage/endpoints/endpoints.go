// Package endpoints implements the storage strategy towards kine for the core v1 Endpoint resource.
package endpoints

import (
	"context"
	"fmt"
	"net/netip"
	"path"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/storage"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
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
	netutils "k8s.io/utils/net"
)

const endpointResource = "endpoints"

const labelManagedBy = "endpoints.kubernetes.io/managed-by"

const controllerName = "endpoint-controller"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

var _ rest.ShortNamesProvider = &REST{}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (*REST) ShortNames() []string {
	return []string{"ep"}
}

// NewEndpointsREST creates a REST interface for corev1 Endpoint resource.
func NewEndpointsREST(storageConfig storagebackend.Config, _ runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(corev1.Resource(endpointResource)),
		func() runtime.Object { return &corev1.Endpoints{} },
		func() runtime.Object { return &corev1.EndpointsList{} },
		"/"+endpointResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	endpointsStrategy := endpointsStrategy{
		runtime.NewScheme(),
		names.SimpleNameGenerator,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &corev1.Endpoints{} },
		NewListFunc:   func() runtime.Object { return &corev1.EndpointsList{} },
		PredicateFunc: EndpointPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + endpointResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+endpointResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: endpointsStrategy,
		UpdateStrategy: endpointsStrategy,
		DeleteStrategy: endpointsStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// EndpointPredicateFunc returns a selection predicate for filtering Endpoint objects.
func EndpointPredicateFunc(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields for a Endpoint object.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	endpoint, ok := obj.(*corev1.Endpoints)
	if !ok {
		return nil, nil, storage.ErrObjectIsNotAnEndpoint
	}

	return labels.Set(endpoint.Labels), SelectableFields(endpoint), nil
}

// SelectableFields returns a field set that can be used for filter selection.
func SelectableFields(obj *corev1.Endpoints) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	endpoint, ok := obj.(*corev1.Endpoints)
	if !ok {
		return "", storage.ErrObjectIsNotAnEndpoint
	}

	return endpoint.Name, nil
}

// ValidateNameFunc validates that the provided name is valid for a given resource type.
// Not all resources have the same validation rules for names. Prefix is true
// if the name will have a value appended to it.  If the name is not valid,
// this returns a list of descriptions of individual characteristics of the
// value that were not valid.  Otherwise this returns an empty list or nil.
type ValidateNameFunc apimachineryvalidation.ValidateNameFunc

// endpointsStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/core/endpoint/strategy.go
type endpointsStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = endpointsStrategy{}
var _ rest.RESTUpdateStrategy = endpointsStrategy{}
var _ rest.RESTDeleteStrategy = endpointsStrategy{}
var _ rest.NamespaceScopedStrategy = endpointsStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (endpointsStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate sets defaults for new objects.
func (endpointsStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {}

// WarningsOnCreate returns warnings for create operations.
func (endpointsStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	endpoint, ok := obj.(*corev1.Endpoints)
	if !ok {
		logger := logging.FromContext(ctx)
		logger.Warn("Expected *corev1.Endpoints", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	return endpointsWarnings(endpoint)
}

// PrepareForUpdate sets defaults for updated objects.
func (endpointsStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

// WarningsOnUpdate returns warnings for update operations.
func (endpointsStrategy) WarningsOnUpdate(ctx context.Context, _, obj runtime.Object) []string {
	endpoint, ok := obj.(*corev1.Endpoints)
	if !ok {
		logger := logging.FromContext(ctx)
		logger.Warn("Expected *corev1.Endpoints", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	return endpointsWarnings(endpoint)
}

// PrepareForDelete clears fields before deletion.
func (endpointsStrategy) PrepareForDelete(_ context.Context, _ runtime.Object) {}

// Validate validates new objects.
func (endpointsStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	endpointObject, ok := obj.(*corev1.Endpoints)
	if !ok {
		logger := logging.FromContext(ctx)
		logger.Warn("Expected *corev1.Endpoints", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	return apimachineryvalidation.ValidateObjectMeta(
		&endpointObject.ObjectMeta, false,
		storage.FieldIsNonNull,
		field.NewPath("metadata"),
	)
}

// ValidateUpdate validates updated objects.
func (endpointsStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	logger := logging.FromContext(ctx)
	endpointObject, success := obj.(*corev1.Endpoints)
	if !success {
		logger.Warn("Expected *corev1.Endpoints for new object", zap.String("actual_type", fmt.Sprintf("%T", obj)))
	}

	oldendpointObject, success := old.(*corev1.Endpoints)
	if !success {
		logger.Warn("Expected *corev1.Endpoints for old object", zap.String("actual_type", fmt.Sprintf("%T", old)))
	}

	return apimachineryvalidation.ValidateObjectMetaUpdate(
		&endpointObject.ObjectMeta,
		&oldendpointObject.ObjectMeta,
		field.NewPath("metadata"),
	)
}

// Canonicalize normalizes objects.
func (endpointsStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (endpointsStrategy) AllowCreateOnUpdate() bool {
	return true
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (endpointsStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// GenerateName generates a name using the given base string.
func (endpointsStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

func endpointsWarnings(endpoints *corev1.Endpoints) []string {
	// Save time by not checking for bad IPs if the request is coming from the
	// Endpoints controller, since we know it fixes up any invalid IPs from its input
	// data when outputting the Endpoints. (The "managed-by" label is new, so this
	// heuristic may fail in skewed clusters, but that just means we won't get the
	// optimization during the skew.)
	if endpoints.Labels[labelManagedBy] == controllerName {
		return nil
	}

	var warnings []string

	//nolint:varnamelen
	for i := range endpoints.Subsets {
		for j := range endpoints.Subsets[i].Addresses {
			fldPath := field.NewPath("subsets").Index(i).Child("addresses").Index(j).Child("ip")

			warnings = append(warnings, GetWarningsForIP(fldPath, endpoints.Subsets[i].Addresses[j].IP)...)
		}

		for j := range endpoints.Subsets[i].NotReadyAddresses {
			fldPath := field.NewPath("subsets").Index(i).Child("notReadyAddresses").Index(j).Child("ip")

			warnings = append(warnings, GetWarningsForIP(fldPath, endpoints.Subsets[i].NotReadyAddresses[j].IP)...)
		}
	}

	return warnings
}

// GetWarningsForIP returns warnings for IP address values in non-standard forms. This
// should only be used with fields that are validated with IsValidIPForLegacyField().
func GetWarningsForIP(fldPath *field.Path, value string) []string {
	//nolint:varnamelen
	ip := netutils.ParseIPSloppy(value)
	if ip == nil {
		return nil
	}

	addr, _ := netip.ParseAddr(value)
	if !addr.IsValid() || addr.Is4In6() {
		// This catches 2 cases: leading 0s (if ParseIPSloppy() accepted it but
		// ParseAddr() doesn't) or IPv4-mapped IPv6 (.Is4In6()). Either way,
		// re-stringifying the net.IP value will give the preferred form.
		return []string{
			fmt.Sprintf("%s: non-standard IP address %q will be considered invalid in a future Kubernetes release: use %q",
				fldPath, value, ip.String()),
		}
	}

	// If ParseIPSloppy() and ParseAddr() both accept it then it's fully valid, though
	// it may be non-canonical.
	if addr.Is6() && addr.String() != value {
		return []string{
			fmt.Sprintf("%s: IPv6 address %q should be in RFC 5952 canonical format (%q)", fldPath, value, addr.String()),
		}
	}

	return nil
}
