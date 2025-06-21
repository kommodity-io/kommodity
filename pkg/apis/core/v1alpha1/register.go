package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the group name used in this package.
	GroupName = "core.kommodity.io"
	// Version is the version used in this package.
	Version = "v1alpha1"
)

// SchemeGroupVersion is group version used to register these objects.
func SchemeGroupVersion() schema.GroupVersion {
	return schema.GroupVersion{Group: GroupName, Version: Version}
}

// Kind takes an unqualified kind and returns back a Group qualified GroupKind.
func Kind(kind string) schema.GroupKind {
	return SchemeGroupVersion().WithKind(kind).GroupKind()
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion().WithResource(resource).GroupResource()
}

// SchemeBuilder initializes a scheme builder for the API group.
func SchemeBuilder() *runtime.SchemeBuilder {
	builder := runtime.NewSchemeBuilder(addKnownTypes)

	return &builder
}

// Adds the list of known types to Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion(),
		&TalosCluster{},
		&TalosClusterList{},
	)

	return nil
}
