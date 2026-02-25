// Package reconciler contains controller reconcilers.
package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	controlplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// PrivateNetworkAnnotation is the annotation key for private network clusters.
	PrivateNetworkAnnotation = "kommodity.io/private-network"

	// privateNetworkRequeueHealthy is the requeue interval when health checks pass.
	privateNetworkRequeueHealthy = 2 * time.Minute

	// privateNetworkRequeueUnhealthy is the requeue interval when health checks fail.
	privateNetworkRequeueUnhealthy = 30 * time.Second

	// leaseGracePeriod is the grace period for lease expiration checks.
	leaseGracePeriod = 60 * time.Second
)

// PrivateNetworkControlPlaneReconciler reconciles TalosControlPlane resources
// for clusters where Talos API is unreachable (private network clusters).
// It performs health checks via Kubernetes API instead of Talos API.
type PrivateNetworkControlPlaneReconciler struct {
	client.Client

	Log     logr.Logger
	Scheme  *runtime.Scheme
	Tracker *remote.ClusterCacheTracker //nolint:staticcheck // Using same API as Siderolabs provider
}

// SetupWithManager sets up the controller with the Manager.
func (r *PrivateNetworkControlPlaneReconciler) SetupWithManager(
	_ context.Context,
	mgr ctrl.Manager,
	options controller.Options,
) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1.TalosControlPlane{}).
		Named("privatenetworkcontrolplane").
		WithEventFilter(predicate.And(
			privateNetworkPredicate(),
			predicate.GenerationChangedPredicate{},
		)).
		WithOptions(options).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to setup PrivateNetworkControlPlane controller: %w", err)
	}

	return nil
}

// privateNetworkPredicate returns a predicate that only matches TalosControlPlane
// resources with the private-network annotation.
func privateNetworkPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			return false
		}

		value, exists := annotations[PrivateNetworkAnnotation]

		return exists && value == "true"
	})
}

// Reconcile handles reconciliation for private network TalosControlPlane resources.
func (r *PrivateNetworkControlPlaneReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	logger := r.Log.WithValues("namespace", req.Namespace, "talosControlPlane", req.Name)
	logger.Info("reconciling private network TalosControlPlane")

	tcp, cluster, result, err := r.fetchControlPlaneResources(ctx, logger, req)
	if err != nil || tcp == nil {
		return result, err
	}

	// Initialize patch helper and perform health checks
	patchHelper, err := patch.NewHelper(tcp, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}

	healthErr := r.reconcileHealth(ctx, logger, cluster, tcp)

	// Patch the TalosControlPlane with updated conditions
	patchErr := patchHelper.Patch(ctx, tcp)
	if patchErr != nil {
		logger.Error(patchErr, "failed to patch TalosControlPlane")

		return ctrl.Result{}, fmt.Errorf("failed to patch TalosControlPlane: %w", patchErr)
	}

	if healthErr != nil {
		logger.Info("health check found issues, will requeue", "error", healthErr.Error())

		return ctrl.Result{RequeueAfter: privateNetworkRequeueUnhealthy}, nil
	}

	logger.Info("private network health checks passed")

	return ctrl.Result{RequeueAfter: privateNetworkRequeueHealthy}, nil
}

func (r *PrivateNetworkControlPlaneReconciler) fetchControlPlaneResources(
	ctx context.Context,
	logger logr.Logger,
	req ctrl.Request,
) (*controlplanev1.TalosControlPlane, *clusterv1.Cluster, ctrl.Result, error) {
	tcp := &controlplanev1.TalosControlPlane{}

	err := r.Get(ctx, req.NamespacedName, tcp)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, ctrl.Result{}, nil
		}

		return nil, nil, ctrl.Result{}, fmt.Errorf("failed to get TalosControlPlane: %w", err)
	}

	// Skip if not a private network cluster or being deleted
	if !isPrivateNetworkCluster(tcp) || !tcp.DeletionTimestamp.IsZero() {
		return nil, nil, ctrl.Result{}, nil
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, tcp.ObjectMeta)
	if err != nil {
		logger.Error(err, "failed to get owner cluster")

		return nil, nil, ctrl.Result{RequeueAfter: privateNetworkRequeueUnhealthy}, nil
	}

	if cluster == nil {
		logger.Info("waiting for cluster owner reference")

		return nil, nil, ctrl.Result{RequeueAfter: privateNetworkRequeueUnhealthy}, nil
	}

	return tcp, cluster, ctrl.Result{}, nil
}

// isPrivateNetworkCluster checks if a TalosControlPlane has the private network annotation.
func isPrivateNetworkCluster(tcp *controlplanev1.TalosControlPlane) bool {
	if tcp.Annotations == nil {
		return false
	}

	value, exists := tcp.Annotations[PrivateNetworkAnnotation]

	return exists && value == "true"
}

// reconcileHealth performs health checks using Kubernetes API and updates conditions.
func (r *PrivateNetworkControlPlaneReconciler) reconcileHealth(
	ctx context.Context,
	logger logr.Logger,
	cluster *clusterv1.Cluster,
	tcp *controlplanev1.TalosControlPlane,
) error {
	// Get workload cluster client
	workloadClient, err := r.Tracker.GetClient(ctx, util.ObjectKey(cluster))
	if err != nil {
		logger.Info("failed to get workload cluster client, cluster may not be ready", "error", err.Error())

		return fmt.Errorf("failed to get workload cluster client: %w", err)
	}

	// Check Kubernetes API endpoints
	endpointHealthy, endpointCount, err := r.checkKubernetesEndpoints(ctx, workloadClient)
	if err != nil {
		logger.Info("failed to check kubernetes endpoints", "error", err.Error())
		conditions.MarkFalse(tcp, controlplanev1.EtcdClusterHealthyCondition,
			"EndpointCheckFailed", clusterv1.ConditionSeverityWarning,
			"Failed to check kubernetes endpoints: %s", err.Error())

		return err
	}

	if !endpointHealthy {
		conditions.MarkFalse(tcp, controlplanev1.EtcdClusterHealthyCondition,
			"NoHealthyEndpoints", clusterv1.ConditionSeverityWarning,
			"No healthy kubernetes API endpoints found")

		return ErrNoHealthyEndpoints
	}

	logger.Info("kubernetes endpoints healthy", "count", endpointCount)

	// Check control plane component leases
	leaseHealthy, err := r.checkControlPlaneLeases(ctx, workloadClient)
	if err != nil {
		logger.Info("failed to check control plane leases", "error", err.Error())
		conditions.MarkFalse(tcp, controlplanev1.ControlPlaneComponentsHealthyCondition,
			"LeaseCheckFailed", clusterv1.ConditionSeverityWarning,
			"Failed to check control plane leases: %s", err.Error())

		return err
	}

	if !leaseHealthy {
		conditions.MarkFalse(tcp, controlplanev1.ControlPlaneComponentsHealthyCondition,
			"LeaseExpired", clusterv1.ConditionSeverityWarning,
			"Control plane component leases are expired")

		return ErrControlPlaneLeaseExpired
	}

	// All checks passed - mark conditions as healthy
	conditions.MarkTrue(tcp, controlplanev1.EtcdClusterHealthyCondition)
	conditions.MarkTrue(tcp, controlplanev1.ControlPlaneComponentsHealthyCondition)

	return nil
}

// checkKubernetesEndpoints verifies the kubernetes API endpoints are healthy.
// Returns (isHealthy, endpointCount, error).
func (r *PrivateNetworkControlPlaneReconciler) checkKubernetesEndpoints(
	ctx context.Context,
	workloadClient client.Client,
) (bool, int, error) {
	endpoints := &corev1.Endpoints{}

	err := workloadClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      "kubernetes",
	}, endpoints)
	if err != nil {
		return false, 0, fmt.Errorf("failed to get kubernetes endpoints: %w", err)
	}

	healthyCount := 0
	for _, subset := range endpoints.Subsets {
		healthyCount += len(subset.Addresses)
	}

	return healthyCount > 0, healthyCount, nil
}

// checkControlPlaneLeases verifies kube-controller-manager and kube-scheduler leases.
func (r *PrivateNetworkControlPlaneReconciler) checkControlPlaneLeases(
	ctx context.Context,
	workloadClient client.Client,
) (bool, error) {
	// Check kube-controller-manager lease
	kcmHealthy, err := r.isLeaseHealthy(ctx, workloadClient, "kube-controller-manager")
	if err != nil {
		return false, fmt.Errorf("kube-controller-manager lease check failed: %w", err)
	}

	if !kcmHealthy {
		return false, nil
	}

	// Check kube-scheduler lease
	schedulerHealthy, err := r.isLeaseHealthy(ctx, workloadClient, "kube-scheduler")
	if err != nil {
		return false, fmt.Errorf("kube-scheduler lease check failed: %w", err)
	}

	if !schedulerHealthy {
		return false, nil
	}

	return true, nil
}

// isLeaseHealthy checks if a specific lease is recent (not expired).
func (r *PrivateNetworkControlPlaneReconciler) isLeaseHealthy(
	ctx context.Context,
	workloadClient client.Client,
	leaseName string,
) (bool, error) {
	lease := &coordinationv1.Lease{}

	err := workloadClient.Get(ctx, types.NamespacedName{
		Namespace: "kube-system",
		Name:      leaseName,
	}, lease)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Lease doesn't exist yet, may be during bootstrap
			return true, nil
		}

		return false, fmt.Errorf("failed to get lease %s: %w", leaseName, err)
	}

	if lease.Spec.RenewTime == nil {
		// No renew time set, consider unhealthy
		return false, nil
	}

	// Check if lease was renewed recently
	// Default lease duration is 15 seconds
	leaseDuration := 15 * time.Second //nolint:mnd // standard Kubernetes lease duration
	if lease.Spec.LeaseDurationSeconds != nil {
		leaseDuration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}

	expirationTime := lease.Spec.RenewTime.Add(leaseDuration + leaseGracePeriod)

	return time.Now().Before(expirationTime), nil
}
