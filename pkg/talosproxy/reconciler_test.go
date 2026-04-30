package talosproxy_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testCIDRAnnotation = "kommodity.io/node-cidr"
)

func newReconcilerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))

	return scheme
}

func newReconcilerProxy(t *testing.T) *talosproxy.Proxy {
	t.Helper()

	cfg := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		IdleTimeout:    time.Second,
	}

	return talosproxy.NewProxy(talosproxy.ProxyDeps{
		Config: cfg,
		Client: fake.NewClientBuilder().Build(),
	})
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

func TestReconciler_RegistersClusterWithValidCIDR(t *testing.T) {
	t.Parallel()

	scheme := newReconcilerScheme(t)
	proxy := newReconcilerProxy(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "ok-cluster",
			Namespace:   "team-a",
			Annotations: map[string]string{testCIDRAnnotation: "10.10.0.0/16"},
		},
	}

	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()

	reconciler := &talosproxy.Reconciler{Client: kubeClient, Proxy: proxy}

	_, err := reconciler.Reconcile(context.Background(), reconcileRequest(cluster.Name, cluster.Namespace))
	require.NoError(t, err)

	entry, lookupErr := proxy.CIDRRegistryForTest().Lookup(net.ParseIP("10.10.0.5"))
	require.NoError(t, lookupErr)
	assert.Equal(t, cluster.Name, entry.ClusterName)
	assert.Equal(t, cluster.Namespace, entry.Namespace)
}

func TestReconciler_InvalidCIDRAnnotationReturnsError(t *testing.T) {
	t.Parallel()

	scheme := newReconcilerScheme(t)
	proxy := newReconcilerProxy(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "bad-cidr",
			Namespace:   "team-b",
			Annotations: map[string]string{testCIDRAnnotation: "not-a-cidr"},
		},
	}

	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()

	reconciler := &talosproxy.Reconciler{Client: kubeClient, Proxy: proxy}

	_, err := reconciler.Reconcile(context.Background(), reconcileRequest(cluster.Name, cluster.Namespace))
	require.Error(t, err)

	assert.Equal(t, 0, proxy.CIDRRegistryForTest().Len(),
		"invalid CIDR must not register the cluster")
}

func TestReconciler_AnnotationRemovalDeregisters(t *testing.T) {
	t.Parallel()

	scheme := newReconcilerScheme(t)
	proxy := newReconcilerProxy(t)

	_, cidr, err := net.ParseCIDR("10.20.0.0/16")
	require.NoError(t, err)
	proxy.RegisterCluster("annot-cluster", "team-c", cidr)
	require.Equal(t, 1, proxy.CIDRRegistryForTest().Len())

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "annot-cluster",
			Namespace: "team-c",
		},
	}

	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()

	reconciler := &talosproxy.Reconciler{Client: kubeClient, Proxy: proxy}

	_, err = reconciler.Reconcile(context.Background(), reconcileRequest(cluster.Name, cluster.Namespace))
	require.NoError(t, err)

	assert.Equal(t, 0, proxy.CIDRRegistryForTest().Len(),
		"removing the annotation must deregister the cluster")
}

func TestReconciler_ClusterDeletionDeregisters(t *testing.T) {
	t.Parallel()

	scheme := newReconcilerScheme(t)
	proxy := newReconcilerProxy(t)

	_, cidr, err := net.ParseCIDR("10.30.0.0/16")
	require.NoError(t, err)
	proxy.RegisterCluster("gone-cluster", "team-d", cidr)
	require.Equal(t, 1, proxy.CIDRRegistryForTest().Len())

	// No Cluster resource exists in the fake client → reconciler hits the NotFound branch.
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &talosproxy.Reconciler{Client: kubeClient, Proxy: proxy}

	_, err = reconciler.Reconcile(context.Background(), reconcileRequest("gone-cluster", "team-d"))
	require.NoError(t, err)

	assert.Equal(t, 0, proxy.CIDRRegistryForTest().Len(),
		"a deleted cluster must be deregistered from the proxy")
}

// Compile-time guard that the reconciler still satisfies the controller-runtime Reconciler interface.
var _ interface {
	Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)
} = (*talosproxy.Reconciler)(nil)
