package reconciler

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/yaml"
)

// ExtraSecretsManagerReconciler reconciles the extra secrets resources.
type ExtraSecretsManagerReconciler struct {
	client.Client
}

// SetupWithManager sets up the reconciler with the provided manager.
func (r *ExtraSecretsManagerReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-extra-secrets-controller").
		For(&corev1.Secret{}).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
			"kommodity-extra-secrets-controller",
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	return nil
}

// Reconcile reconciles ExtraSecretsManagerReconciler resources.
func (r *ExtraSecretsManagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Reconciling ExtraSecretsManager for Secret", zap.String("secret", req.String()))

	extraSecretSecret := &corev1.Secret{}

	err := r.Get(ctx, req.NamespacedName, extraSecretSecret)
	if err != nil {
		logger.Error("Failed to get Extra Secret", zap.String("secret", req.String()), zap.Error(err))

		return ctrl.Result{}, fmt.Errorf("failed to get Secret %s: %w", req.String(), client.IgnoreNotFound(err))
	}

	clusterName, success := extraSecretSecret.Labels["cluster.x-k8s.io/cluster-name"]
	if !success {
		logger.Error("ClusterName label not found in Secret", zap.String("secret", req.String()))

		return ctrl.Result{}, fmt.Errorf("clusterName %w: %s", ErrValueNotFoundInSecret, req.String())
	}

	kubeClient, err := (&DownstreamClientConfig{
		Client:      r.Client,
		ClusterName: clusterName,
	}).FetchDownstreamKubernetesClient(ctx)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: RequeueAfter,
		}, fmt.Errorf("failed to fetch kubeconfig from secret: %w", err)
	}

	for key, value := range extraSecretSecret.StringData {
		logger.Info("Reconciling Extra Secret Data", zap.String("key", key))

		secret := &corev1.Secret{}

		err := yaml.Unmarshal([]byte(value), &secret)
		if err != nil {
			logger.Error("Failed to unmarshal Extra Secret Data", zap.String("key", key), zap.Error(err))

			return ctrl.Result{}, fmt.Errorf("failed to unmarshal Extra Secret Data for key %s: %w", key, err)
		}

		err = ApplySecretToClient(ctx, kubeClient, secret)
		if err != nil {
			logger.Error("Failed to apply Extra Secret to client", zap.String("key", key), zap.Error(err))

			return ctrl.Result{
				Requeue:      true,
				RequeueAfter: RequeueAfter,
			}, fmt.Errorf("failed to apply Extra Secret to client for key %s: %w", key, err)
		}
	}

	return ctrl.Result{}, nil
}
