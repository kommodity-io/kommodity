package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TalosClusterList is a list of TalosCluster resources.
type TalosClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []TalosCluster `json:"items"`
}
