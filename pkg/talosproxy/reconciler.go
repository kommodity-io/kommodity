package talosproxy

import (
	"context"
	"fmt"
	"net"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	// nodeCIDRAnnotation is the annotation on Cluster resources that specifies the node CIDR.
	nodeCIDRAnnotation = "kommodity.io/node-cidr"
	// talosProxyControllerName is the name of the talos proxy controller.
	talosProxyControllerName = "kommodity-talos-proxy-controller"
)

// Reconciler watches Cluster resources for the node-cidr annotation
// and registers/deregisters clusters with the proxy.
type Reconciler struct {
	client.Client

	Proxy *Proxy
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *Reconciler) SetupWithManager(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named(talosProxyControllerName).
		For(&clusterv1.Cluster{}).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPaused(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up talos proxy controller with manager: %w", err)
	}

	return nil
}

// Reconcile handles Cluster resource changes, registering or deregistering
// clusters with the proxy based on the presence of the node-cidr annotation.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	logger.Info("Reconciling Cluster for TalosProxy",
		zap.String("cluster", req.String()))

	cluster := &clusterv1.Cluster{}

	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.handleClusterDeletion(logger, req.Name)

			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get Cluster %s: %w", req.String(), err)
	}

	// Check for deletion
	if !cluster.DeletionTimestamp.IsZero() {
		r.handleClusterDeletion(logger, cluster.Name)

		return ctrl.Result{}, nil
	}

	return r.handleClusterRegistration(logger, cluster)
}

func (r *Reconciler) handleClusterDeletion(
	logger *zap.Logger,
	clusterName string,
) {
	logger.Info("Cluster deleted, deregistering from proxy",
		zap.String("cluster", clusterName))

	r.Proxy.DeregisterCluster(clusterName)
}

func (r *Reconciler) handleClusterRegistration(
	logger *zap.Logger,
	cluster *clusterv1.Cluster,
) (ctrl.Result, error) {
	annotations := cluster.GetAnnotations()
	cidrStr, hasCIDR := annotations[nodeCIDRAnnotation]

	if !hasCIDR {
		r.Proxy.DeregisterCluster(cluster.Name)

		return ctrl.Result{}, nil
	}

	_, cidr, err := net.ParseCIDR(cidrStr)
	if err != nil {
		logger.Error("Failed to parse node-cidr annotation",
			zap.String("cluster", cluster.Name),
			zap.String("cidr", cidrStr),
			zap.Error(err))

		return ctrl.Result{}, fmt.Errorf("failed to parse CIDR %s for cluster %s: %w",
			cidrStr, cluster.Name, err)
	}

	logger.Info("Registering cluster with proxy",
		zap.String("cluster", cluster.Name),
		zap.String("namespace", cluster.Namespace),
		zap.String("cidr", cidr.String()))

	r.Proxy.RegisterCluster(cluster.Name, cluster.Namespace, cidr)

	return ctrl.Result{}, nil
}
