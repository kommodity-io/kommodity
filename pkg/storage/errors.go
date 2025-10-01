// Package storage provides error definitions for storage operations.
package storage

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

var (
	// ErrObjectIsNotANamespace indicates that the object is not a namespace.
	ErrObjectIsNotANamespace = errors.New("object is not a Namespace")
	// ErrObjectIsNotASecret indicates that the object is not a secret.
	ErrObjectIsNotASecret = errors.New("object is not a Secret")
	// ErrObjectIsNotAConfigMap indicates that the object is not a ConfigMap.
	ErrObjectIsNotAConfigMap = errors.New("object is not a ConfigMap")
	// ErrObjectIsNotAService indicates that the object is not a service.
	ErrObjectIsNotAService = errors.New("object is not a Service")
	// ErrObjectIsNotAnEndpoint indicates that the object is not an endpoint.
	ErrObjectIsNotAnEndpoint = errors.New("object is not an Endpoint")
	// ErrObjectIsNotAnEvent indicates that the object is not an event.
	ErrObjectIsNotAnEvent = errors.New("object is not an Event")
	// ErrObjectIsNotASelfSubjectAccessReview indicates that the object is not a SelfSubjectAccessReview.
	ErrObjectIsNotASelfSubjectAccessReview = errors.New("object is not a SelfSubjectAccessReview")
	// ErrObjectIsNotASubjectAccessReview indicates that the object is not a SubjectAccessReview.
	ErrObjectIsNotASubjectAccessReview = errors.New("object is not a SubjectAccessReview")
	// ErrFieldIsNull indicates that the field is null.
	ErrFieldIsNull = errors.New("field must not be null")
)

// ExpectedGot returns an string wrapping the expected error with the actual type of the object.
func ExpectedGot(expected error, got runtime.Object) string {
	return fmt.Sprintf("%s, got %T", expected.Error(), got)
}
