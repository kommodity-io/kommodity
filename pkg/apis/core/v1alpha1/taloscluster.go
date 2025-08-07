package v1alpha1

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TalosCluster is a specification for a TalosCluster resource.
type TalosCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification for the TalosCluster resource.
	Spec TalosClusterSpec `json:"spec"`
}

// TalosClusterSpec is the spec for a TalosCluster resource.
type TalosClusterSpec struct {
	// KubernetesVersion is the version of Kubernetes to use in the cluster.
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`
	// TalosVersion is the version of Talos to use in the cluster.
	TalosVersion string `json:"talosVersion,omitempty"`
}

var _ resource.Object = &TalosCluster{}

func (t TalosCluster) New() runtime.Object {
	return &TalosCluster{}
}

func (t TalosCluster) NewList() runtime.Object {
	return &TalosClusterList{}
}

func (t TalosCluster) GetGroupVersionResource() schema.GroupVersionResource {
	return SchemeGroupVersion().WithResource("talosclusters")
}

func (t *TalosCluster) GetObjectMeta() *metav1.ObjectMeta {
	return &t.ObjectMeta
}

func (t *TalosCluster) NamespaceScoped() bool {
	return true
}

func (t *TalosCluster) IsStorageVersion() bool {
	return true
}

// Implement ShortNamesProvider to return short names for the resource.
func (t TalosCluster) ShortNames() []string {
	return []string{"tc"}
}

// TalosClusterList is a list of TalosCluster resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type TalosClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []TalosCluster `json:"items"`
}

func (_ TalosCluster) CreateValidation(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (_ TalosCluster) DeleteValidation(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (_ TalosCluster) UpdateValidation(ctx context.Context, obj, old runtime.Object) error {
	return nil
}

func (t TalosCluster) Preconditions() *metav1.Preconditions {
	// t is the newObject
	return nil
}

func (t TalosCluster) UpdatedObject(ctx context.Context, oldObj runtime.Object) (newObj runtime.Object, err error) {
	// t is the newObject, oldObj is the existing object.
	return nil, nil
}
