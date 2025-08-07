package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
)

type Validatable interface {
	CreateValidation(ctx context.Context, obj runtime.Object) error
	UpdateValidation(ctx context.Context, obj, old runtime.Object) error
	DeleteValidation(ctx context.Context, obj runtime.Object) error
}
