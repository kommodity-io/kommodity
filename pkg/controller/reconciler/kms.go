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
)

// KMSClusterReconciler keeps the KMS Router's per-cluster handler map in sync
// with the set of CAPI Cluster resources. Each Cluster CR gets one handler;
// when the Cluster is deleted, the handler is removed.
type KMSClusterReconciler struct {
	client.Client

	Router *kms.Router
}

// SetupWithManager wires the reconciler to watch CAPI Cluster resources.
func (r *KMSClusterReconciler) SetupWithManager(_ context.Context,
	mgr ctrl.Manager, opt controller.Options,
) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-kms-cluster-controller").
		For(&clusterv1.Cluster{}).
		WithOptions(opt)

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up KMSClusterReconciler with a controller manager: %w", err)
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
