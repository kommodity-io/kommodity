//nolint:testpackage // white-box: shares the webhook package's test manager helper
package webhook

import (
	"testing"

	bootstrapv1alpha2 "github.com/siderolabs/cluster-api-bootstrap-provider-talos/api/v1alpha2"
	bootstrapv1alpha3 "github.com/siderolabs/cluster-api-bootstrap-provider-talos/api/v1alpha3"
	controlplanev1alpha3 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	capzv1beta1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capzexpv1beta1 "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta1"
	addonsv1 "sigs.k8s.io/cluster-api/api/addons/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capiexpv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	capiexpipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	capiruntimev1 "sigs.k8s.io/cluster-api/exp/runtime/api/v1alpha1"
)

// newWebhookTestScheme assembles the scheme every provider webhook registration needs:
// the webhook builders resolve the GVK of the type they register against the manager's
// scheme, so all provider schemes must be present (mirroring what the aggregated server
// builds via provider.AddAllProvidersToScheme).
func newWebhookTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()

	addFuncs := []struct {
		name string
		fn   func(*runtime.Scheme) error
	}{
		{"clientgoscheme.AddToScheme", clientgoscheme.AddToScheme},
		{"capi v1beta1", capiv1.AddToScheme},
		{"capi addons v1beta1", addonsv1.AddToScheme},
		{"capi exp v1beta1", capiexpv1.AddToScheme},
		{"capi exp runtime v1alpha1", capiruntimev1.AddToScheme},
		{"capi exp ipam v1beta1", capiexpipamv1.AddToScheme},
		{"capz v1beta1", capzv1beta1.AddToScheme},
		{"capz exp v1beta1", capzexpv1beta1.AddToScheme},
		{"cabpt v1alpha2", bootstrapv1alpha2.AddToScheme},
		{"cabpt v1alpha3", bootstrapv1alpha3.AddToScheme},
		{"cacppt v1alpha3", controlplanev1alpha3.AddToScheme},
	}

	for _, add := range addFuncs {
		err := add.fn(scheme)
		if err != nil {
			t.Fatalf("failed to add %s to scheme: %v", add.name, err)
		}
	}

	return scheme
}

// unreachableRESTConfig returns a REST config pointing at an unroutable loopback URL.
// Manager construction never dials it (clients, mappers and caches are lazy), and the
// webhook server only binds when the manager starts — which these tests never do.
func unreachableRESTConfig() *rest.Config {
	return &rest.Config{Host: "http://127.0.0.1:1"}
}
