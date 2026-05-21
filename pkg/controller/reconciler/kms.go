package reconciler

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// KMSClusterReconciler keeps the KMS Router's per-cluster handler map in sync
// with the set of CAPI Cluster resources. Each Cluster CR gets one handler;
// when the Cluster is deleted, the handler is removed.
type KMSClusterReconciler struct {
	client.Client

	Router *kms.Router
}

// SetupWithManager wires the reconciler to watch CAPI Cluster resources and
// primes the Router with handlers for all existing Clusters so the gRPC server
// doesn't return NotFound during the window between startup and the first
// reconcile cycle.
func (r *KMSClusterReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options,
) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-kms-cluster-controller").
		For(&clusterv1.Cluster{}).
		WithOptions(opt).
		// Only requeue when Spec changes; we don't care about Status churn from
		// machine rollouts. Create and Delete events bypass this predicate, so
		// onboarding and offboarding still work normally.
		WithEventFilter(predicate.GenerationChangedPredicate{})

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up KMSClusterReconciler with a controller manager: %w", err)
	}

	err = r.primeRouter(ctx, mgr.GetAPIReader())
	if err != nil {
		return fmt.Errorf("failed to prime KMS router: %w", err)
	}

	return nil
}

// Reconcile registers or deregisters a per-cluster KMS handler based on the
// presence and deletion timestamp of the Cluster CR.
func (r *KMSClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	cluster := &clusterv1.Cluster{}

	err := r.Get(ctx, req.NamespacedName, cluster)
	if apierrors.IsNotFound(err) {
		r.Router.Deregister(req.Name)
		logger.Info("Deregistered KMS handler for deleted cluster",
			zap.String("cluster", req.Name))

		return ctrl.Result{}, nil
	}

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Cluster %s: %w", req.String(), err)
	}

	// Deregister as soon as deletion starts: any in-flight Unseal calls will
	// 404 briefly, but Talos retries and the cluster's machines are draining
	// anyway, so no fresh Seal calls are expected during this window.
	if !cluster.DeletionTimestamp.IsZero() {
		r.Router.Deregister(cluster.Name)
		logger.Info("Deregistered KMS handler for cluster being deleted",
			zap.String("cluster", cluster.Name))

		return ctrl.Result{}, nil
	}

	r.Router.Register(cluster.Name)
	logger.Info("Registered KMS handler for cluster",
		zap.String("cluster", cluster.Name))

	return ctrl.Result{}, nil
}

// primeRouter performs a one-shot uncached list of Cluster CRs and registers a
// handler for each. The manager's cache is not yet started at this point, so we
// read directly from the API server via the APIReader.
func (r *KMSClusterReconciler) primeRouter(ctx context.Context, reader client.Reader) error {
	logger := logging.FromContext(ctx)

	var clusters clusterv1.ClusterList

	err := reader.List(ctx, &clusters)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}

		r.Router.Register(cluster.Name)
	}

	logger.Info("Primed KMS router with existing clusters",
		zap.Int("count", len(clusters.Items)))

	return nil
}
