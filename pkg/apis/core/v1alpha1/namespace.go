package v1alpha1

import (
	"context"

	"github.com/kommodity-io/kommodity/pkg/validation"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
)

// Namespace represents a Kubernetes namespace and is a wrapper around corev1.Namespace.
// It implements the rest.Storage interface for handling namespace resources.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
type Namespace struct {
	*corev1.Namespace
}

var _ rest.ShortNamesProvider = &Namespace{}
var _ rest.UpdatedObjectInfo = &Namespace{}
var _ validation.Validatable = &Namespace{}

// GetGroupVersionResource returns the GroupVersionResource for Namespace and is fulfilling runtime.Object interface.
func (t Namespace) GetGroupVersionResource() schema.GroupVersionResource {
	return SchemeGroupVersion().WithResource("namespaces")
}

// GetObjectMeta returns the ObjectKind for Namespace and is fulfilling runtime.Object interface.
func (t Namespace) GetObjectMeta() *metav1.ObjectMeta {
	return &t.ObjectMeta
}

// NamespaceScoped returns true indicating that Namespace is a namespaced resource.
func (t Namespace) NamespaceScoped() bool {
	return false
}

// IsStorageVersion returns true indicating that Namespace is a storage version resource.
func (t Namespace) IsStorageVersion() bool {
	return true
}

// New returns a new Namespace object.
//
//nolint:ireturn
func (t Namespace) New() runtime.Object {
	return &Namespace{}
}

// ShortNames implement ShortNamesProvider to return short names for the resource.
func (t Namespace) ShortNames() []string {
	return []string{"ns"}
}

// CreateValidation implements Validatable interface and is used for validating the creation of a Namespace.
func (Namespace) CreateValidation(_ context.Context, _ runtime.Object) error {
	// Intentionally left blank
	return nil
}

// DeleteValidation implements Validatable interface and is used for validating the deletion of a Namespace.
func (Namespace) DeleteValidation(_ context.Context, _ runtime.Object) error {
	// Intentionally left blank
	return nil
}

// Preconditions returns preconditions built from the updated object, if applicable.
func (Namespace) Preconditions() *metav1.Preconditions {
	// Intentionally left blank
	return nil
}

// UpdateValidation implements Validatable interface and is used for validating the update of a Namespace.
func (Namespace) UpdateValidation(_ context.Context, obj, old runtime.Object) error {
	oldNamespace, success := old.(Namespace)
	if !success {
		return ErrNotOfTypeNamespace
	}

	newNamespace, success := obj.(Namespace)
	if !success {
		return ErrNotOfTypeNamespace
	}

	if oldNamespace.Name != newNamespace.Name {
		return ErrNameCannotBeChanged
	}

	return nil
}

// UpdatedObject implements rest.UpdatedObjectInfo interface and is used to update the Namespace object.
//
//nolint:ireturn
func (t Namespace) UpdatedObject(_ context.Context, _ runtime.Object) (runtime.Object, error) {
	// t is the newObject, oldObj is the existing object.
	return t, nil
}

// NamespaceList is a list of Namespace resources.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NamespaceList struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Namespace `json:"items"`
}

// DeepCopyObject returns a deep copy of the NamespaceList object.
//
//nolint:ireturn
func (in *NamespaceList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(NamespaceList)
	*out = *in
	out.Items = make([]Namespace, len(in.Items))

	copy(out.Items, in.Items)

	return out
}

// NewList returns a new NamespaceList object.
//
//nolint:ireturn
func (t Namespace) NewList() runtime.Object {
	return &NamespaceList{}
}
