// Package validation provides interfaces and methods for validating Kubernetes API objects during REST requests.
package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
)

// Validatable is an interface that defines methods for validating objects 
// as part of REST requests to the Kubernetes API Server.
type Validatable interface {
	CreateValidation(ctx context.Context, obj runtime.Object) error
	UpdateValidation(ctx context.Context, obj, old runtime.Object) error
	DeleteValidation(ctx context.Context, obj runtime.Object) error
}
