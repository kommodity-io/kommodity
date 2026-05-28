// Package deployments provides a stub REST storage for apps/v1 Deployments.
//
// Kommodity does not host workloads on its own API server, so apps/v1 is not
// installed for real. However, upstream cluster-api-provider-kubevirt's
// KubevirtCluster reconcileDelete lists DeploymentList in the management
// cluster to clean up "extra-resource" labeled KCCM deployments. When apps/v1
// is not served, the list returns NoMatchError and reconcileDelete aborts
// before removing the KubevirtCluster finalizer, leaving the resource stuck.
//
// This stub installs a discoverable apps/v1/deployments endpoint that always
// returns an empty list, so the cleanup loop succeeds without persisting any
// state.
package deployments

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
)

const (
	deploymentResource     = "deployments"
	deploymentSingularName = "deployment"
)

// REST is an empty stub serving apps/v1 Deployments. Only List is implemented
// and always returns an empty DeploymentList.
type REST struct{}

var (
	_ rest.Storage              = (*REST)(nil)
	_ rest.Scoper               = (*REST)(nil)
	_ rest.Lister               = (*REST)(nil)
	_ rest.SingularNameProvider = (*REST)(nil)
	_ rest.TableConvertor       = (*REST)(nil)
)

// NewREST constructs a new stub Deployment REST storage.
func NewREST() rest.Storage {
	return &REST{}
}

// New returns a new empty Deployment object.
func (*REST) New() runtime.Object {
	return &appsv1.Deployment{}
}

// NewList returns a new empty DeploymentList object.
func (*REST) NewList() runtime.Object {
	return &appsv1.DeploymentList{}
}

// NamespaceScoped reports that Deployments are namespaced.
func (*REST) NamespaceScoped() bool {
	return true
}

// GetSingularName returns the resource's singular name.
func (*REST) GetSingularName() string {
	return deploymentSingularName
}

// Destroy is a no-op; the stub holds no resources.
func (*REST) Destroy() {}

// List always returns an empty DeploymentList.
func (*REST) List(_ context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	return &appsv1.DeploymentList{
		ListMeta: metav1.ListMeta{ResourceVersion: "0"},
		Items:    []appsv1.Deployment{},
	}, nil
}

// ConvertToTable converts the result to a table form.
func (*REST) ConvertToTable(
	ctx context.Context,
	obj runtime.Object,
	tableOptions runtime.Object,
) (*metav1.Table, error) {
	table, err := rest.NewDefaultTableConvertor(appsv1.Resource(deploymentResource)).
		ConvertToTable(ctx, obj, tableOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to convert deployment list to table: %w", err)
	}

	return table, nil
}
