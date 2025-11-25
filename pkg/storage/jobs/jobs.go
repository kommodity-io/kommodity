// Package jobs implements the storage strategy towards kine for the core v1 Job resource.
package jobs

import (
	"context"
	"fmt"
	"log"
	"path"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"

	"github.com/kommodity-io/kommodity/pkg/storage"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	apistorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

const jobResource = "jobs"

// REST wraps a Store and implements rest.Scoper.
type REST struct {
	*genericregistry.Store
}

// StatusREST implements the REST endpoint for changing the status of a resourcequota.
type StatusREST struct {
	store *genericregistry.Store
}

// NewJobsREST creates a REST interface for corev1 Job resource.
func NewJobsREST(storageConfig storagebackend.Config, scheme runtime.Scheme) (rest.Storage, error) {
	store, _, err := factory.Create(
		*storageConfig.ForResource(batchv1.Resource(jobResource)),
		func() runtime.Object { return &batchv1.Job{} },
		func() runtime.Object { return &batchv1.JobList{} },
		"/"+jobResource,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", err)
	}

	dryRunnableStorage := genericregistry.DryRunnableStorage{
		Storage: store,
		Codec:   storageConfig.Codec,
	}

	jobStrategy := jobStrategy{
		scheme: scheme,
	}

	restStore := &genericregistry.Store{
		NewFunc:       func() runtime.Object { return &batchv1.Job{} },
		NewListFunc:   func() runtime.Object { return &batchv1.JobList{} },
		PredicateFunc: JobPredicateFunc,
		KeyRootFunc:   func(_ context.Context) string { return "/" + jobResource },
		KeyFunc: func(_ context.Context, name string) (string, error) {
			return path.Join("/"+jobResource, name), nil
		},
		ObjectNameFunc: ObjectNameFunc,
		CreateStrategy: jobStrategy,
		UpdateStrategy: jobStrategy,
		DeleteStrategy: jobStrategy,
		Storage:        dryRunnableStorage,
	}

	return &REST{restStore}, nil
}

// JobPredicateFunc returns a selection predicate for filtering Job objects.
func JobPredicateFunc(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields of a given object for filtering purposes.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a job.")
	}
	return labels.Set(job.ObjectMeta.Labels), SelectableFields(job), nil
}

// JobSelectableFields returns a field set that represents the object for matching purposes.
func SelectableFields(job *batchv1.Job) fields.Set {
	objectMetaFieldsSet := generic.ObjectMetaFieldsSet(&job.ObjectMeta, true)
	specificFieldsSet := fields.Set{
		"status.successful": strconv.Itoa(int(job.Status.Succeeded)),
	}
	return generic.MergeFieldsSets(objectMetaFieldsSet, specificFieldsSet)
}

// ObjectNameFunc returns the name of the object.
func ObjectNameFunc(obj runtime.Object) (string, error) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return "", fmt.Errorf("object is not a Job")
	}

	return job.Name, nil
}

// jobStrategy implements RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy
// Heavily inspired by: https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/batch/job/strategy.go
type jobStrategy struct {
	scheme runtime.Scheme
}

var _ rest.RESTCreateStrategy = jobStrategy{}
var _ rest.RESTUpdateStrategy = jobStrategy{}
var _ rest.RESTDeleteStrategy = jobStrategy{}
var _ rest.NamespaceScopedStrategy = jobStrategy{}

// NamespaceScoped tells the apiserver if the resource lives in a namespace.
func (jobStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate clears the status of a job before creation.
func (jobStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		log.Printf("expected *batchv1.Job, got %T", obj)

		return
	}

	job.Status = batchv1.JobStatus{}

	job.Generation = 1
}

// WarningsOnCreate returns warnings for create operations.
func (jobStrategy) WarningsOnCreate(_ context.Context, obj runtime.Object) []string {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return []string{storage.ExpectedGot(storage.ErrObjectIsNotASecret, obj)}
	}

	return warningsForJob(job)
}

func warningsForJob(job *batchv1.Job) []string {
	var warnings []string
	// Add job-specific warnings here if needed
	return warnings
}

// PrepareForUpdate sets defaults for updated objects.
func (jobStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newJob, success := obj.(*batchv1.Job)
	if !success {
		log.Printf("expected *batchv1.Job, got %T", obj)

		return
	}

	oldJob, success := old.(*batchv1.Job)
	if !success {
		log.Printf("expected *batchv1.Job, got %T", obj)

		return
	}

	// Any changes to the spec increment the generation number.
	// See metav1.ObjectMeta description for more information on Generation.
	if !apiequality.Semantic.DeepEqual(newJob.Spec, oldJob.Spec) {
		newJob.Generation = oldJob.Generation + 1
	}
}

// WarningsOnUpdate returns warnings for update operations.
func (jobStrategy) WarningsOnUpdate(_ context.Context, _, obj runtime.Object) []string {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return []string{storage.ExpectedGot(storage.ErrObjectIsNotAJob, obj)}
	}

	return warningsForJob(job)
}

func (jobStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	jobObject, success := obj.(*batchv1.Job)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAJob.Error())}
	}

	return validation.ValidateObjectMeta(
		&jobObject.ObjectMeta,
		true,
		validation.ValidateServiceAccountName,
		field.NewPath("metadata"))
}

func (jobStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newJobObject, success := obj.(*batchv1.Job)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), obj,
			storage.ErrObjectIsNotAJob.Error())}
	}

	oldJobObject, success := old.(*batchv1.Job)
	if !success {
		return field.ErrorList{field.Invalid(
			field.NewPath("object"), old,
			storage.ErrObjectIsNotAJob.Error())}
	}

	allErrors := validation.ValidateObjectMetaUpdate(
		&newJobObject.ObjectMeta,
		&oldJobObject.ObjectMeta,
		field.NewPath("metadata"))

	allErrors = append(allErrors, validation.ValidateObjectMeta(
		&newJobObject.ObjectMeta,
		true,
		validation.ValidateServiceAccountName,
		field.NewPath("metadata"))...)

	return allErrors
}

// Canonicalize normalizes objects.
func (jobStrategy) Canonicalize(_ runtime.Object) {}

// AllowCreateOnUpdate determines if create is allowed on update.
func (jobStrategy) AllowCreateOnUpdate() bool {
	return false
}

// AllowUnconditionalUpdate determines if update can ignore resource version.
func (jobStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// GenerateName generates a name using the given base string.
func (jobStrategy) GenerateName(base string) string {
	return names.SimpleNameGenerator.GenerateName(base)
}

// ObjectKinds returns the GroupVersionKind for the object.
func (ss jobStrategy) ObjectKinds(obj runtime.Object) ([]schema.GroupVersionKind, bool, error) {
	gvks, unversioned, err := ss.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, unversioned, fmt.Errorf("failed to get object kinds for %T: %w", obj, err)
	}

	return gvks, unversioned, nil
}

// Recognizes returns true if this strategy handles the given GroupVersionKind.
func (ss jobStrategy) Recognizes(gvk schema.GroupVersionKind) bool {
	return ss.scheme.Recognizes(gvk)
}
