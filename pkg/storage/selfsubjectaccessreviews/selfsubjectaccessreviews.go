// Package selfsubjectaccessreviews implements a REST storage for SelfSubjectAccessReview.
package selfsubjectaccessreviews

import (
	"context"

	"github.com/kommodity-io/kommodity/pkg/storage"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	authz "k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

// SelfSubjectAccessReviewREST implements the SSAR REST endpoint.
type SelfSubjectAccessReviewREST struct {
	Authorizer authz.Authorizer
}

//nolint:misspell // Creater is spelled correctly as per K8s interfaces.
var _ rest.Creater = &SelfSubjectAccessReviewREST{}
var _ rest.Storage = &SelfSubjectAccessReviewREST{}
var _ rest.Scoper = &SelfSubjectAccessReviewREST{}
var _ rest.SingularNameProvider = &SelfSubjectAccessReviewREST{}

// Destroy implements rest.Storage. No resources to tear down.
func (r *SelfSubjectAccessReviewREST) Destroy() {}

// New returns an empty SSAR object.
func (r *SelfSubjectAccessReviewREST) New() runtime.Object {
	return &authorizationv1.SelfSubjectAccessReview{}
}

// NamespaceScoped implements rest.Scoper. SSAR is not namespaced.
func (r *SelfSubjectAccessReviewREST) NamespaceScoped() bool {
	return false
}

// GetSingularName returns the singular name of the resource.
func (r *SelfSubjectAccessReviewREST) GetSingularName() string {
	return "selfsubjectaccessreview"
}

// Create evaluates the caller's access based on SSAR spec.
func (r *SelfSubjectAccessReviewREST) Create(
	ctx context.Context,
	obj runtime.Object,
	_ rest.ValidateObjectFunc,
	_ *metav1.CreateOptions,
) (runtime.Object, error) {
	ssar, success := obj.(*authorizationv1.SelfSubjectAccessReview)
	if !success {
		return nil, storage.ErrObjectIsNotASelfSubjectAccessReview
	}

	// SSAR always evaluates the *requesting* user ("self"), not a provided user.
	user, success := request.UserFrom(ctx)
	if !success || user == nil {
		// Don't 500; return a completed object with denied status.
		ssar.Status.Allowed = false
		ssar.Status.Denied = true
		ssar.Status.Reason = "no user on context"
		ssar.Status.EvaluationError = "no user on context"

		return ssar, nil
	}

	attrs := getUserAttributes(ssar)
	if attrs == nil {
		// No attributes provided; deny safely.
		ssar.Status.Allowed = false
		ssar.Status.Denied = true
		ssar.Status.Reason = "no attributes provided"

		return ssar, nil
	}

	decision, reason, err := r.Authorizer.Authorize(ctx, attrs)

	ssar.Status.Allowed = decision == authz.DecisionAllow
	ssar.Status.Denied = decision == authz.DecisionDeny
	ssar.Status.Reason = reason

	if err != nil {
		ssar.Status.EvaluationError = err.Error()
	}

	return ssar, nil
}

func getUserAttributes(ssar *authorizationv1.SelfSubjectAccessReview) *authz.AttributesRecord {
	switch {
	case ssar.Spec.ResourceAttributes != nil:
		resourceAttributes := ssar.Spec.ResourceAttributes

		return &authz.AttributesRecord{
			Verb:            resourceAttributes.Verb,
			APIGroup:        resourceAttributes.Group,
			Resource:        resourceAttributes.Resource,
			Subresource:     resourceAttributes.Subresource,
			Name:            resourceAttributes.Name,
			Namespace:       resourceAttributes.Namespace,
			ResourceRequest: true,
		}
	case ssar.Spec.NonResourceAttributes != nil:
		nra := ssar.Spec.NonResourceAttributes

		return &authz.AttributesRecord{
			Verb:            nra.Verb,
			Path:            nra.Path,
			ResourceRequest: false,
		}
	}

	return nil
}
