package v1alpha1

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
)

type Namespace struct {
	*corev1.Namespace
}

var _ rest.ShortNamesProvider = &Namespace{}

func (t Namespace) GetGroupVersionResource() schema.GroupVersionResource {
	return SchemeGroupVersion().WithResource("namespaces")
}

func (t *Namespace) GetObjectMeta() *metav1.ObjectMeta {
	return &t.ObjectMeta
}

func (t *Namespace) NamespaceScoped() bool {
	return false
}

func (t *Namespace) IsStorageVersion() bool {
	return true
}

func (t Namespace) New() runtime.Object {
	return &Namespace{}
}

// Implement ShortNamesProvider to return short names for the resource.
func (t Namespace) ShortNames() []string {
	return []string{"ns"}
}

func (_ Namespace) CreateValidation(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (_ Namespace) DeleteValidation(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (_ Namespace) UpdateValidation(ctx context.Context, obj, old runtime.Object) error {
	return nil
}

func (t Namespace) Preconditions() *metav1.Preconditions {
	// t is the newObject
	return nil
}

func (t Namespace) UpdatedObject(ctx context.Context, oldObj runtime.Object) (newObj runtime.Object, err error) {
	// t is the newObject, oldObj is the existing object.
	return nil, nil
}

type NamespaceList struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Namespace `json:"items"`
}

func (in *NamespaceList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(NamespaceList)
	*out = *in
	out.Items = make([]Namespace, len(in.Items))

	for i := range in.Items {
		out.Items[i] = in.Items[i]
	}

	return out
}

func (t Namespace) NewList() runtime.Object {
	return &NamespaceList{}
}
