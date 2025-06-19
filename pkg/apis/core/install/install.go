// Package install contains functions to register the API group and add types to a scheme.
package install

import (
	"github.com/kommodity-io/kommodity/pkg/apis/core/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// Install registers the API group and adds types to a scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(v1alpha1.SchemeBuilder().AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(v1alpha1.SchemeGroupVersion()))
}
