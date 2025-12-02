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
)

type cloudControllerManagerConfig struct {
	client.Client
	DownstreamClientConfig

	SecretName string
}

func (c *cloudControllerManagerConfig) copySecret(ctx context.Context, downstreamClient client.Client) error {
	providerSecret := &corev1.Secret{}

	err := c.Get(ctx, client.ObjectKey{
		Name:      c.SecretName,
		Namespace: "default",
	}, providerSecret)
	if err != nil {
		return fmt.Errorf("failed to get provider secret %s: %w", c.SecretName, err)
	}

	providerSecret.Namespace = "kube-system"

	err = ApplySecretToClient(ctx, downstreamClient, providerSecret)
	if err != nil {
		return fmt.Errorf("failed to apply provider secret %s to downstream cluster: %w", c.SecretName, err)
	}

	return nil
}

// CloudControllerManagerReconciler reconciles CloudControllerManager resources.
type CloudControllerManagerReconciler struct {
	client.Client
}

// SetupWithManager sets up the reconciler with the provided manager.
func (r *CloudControllerManagerReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-ccm-controller").
		For(&corev1.ConfigMap{}).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
			"kommodity-ccm-controller",
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	return nil
}

// Reconcile reconciles CloudControllerManager resources.
func (r *CloudControllerManagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Reconciling CloudControllerManager for ConfigMap", zap.String("configmap", req.String()))

	ccmConfigMap := &corev1.ConfigMap{}

	err := r.Get(ctx, req.NamespacedName, ccmConfigMap)
	if err != nil {
		logger.Error("Failed to get ConfigMap", zap.String("configmap", req.String()), zap.Error(err))

		return ctrl.Result{}, fmt.Errorf("failed to get ConfigMap %s: %w", req.String(), client.IgnoreNotFound(err))
	}

	clusterName, success := ccmConfigMap.Labels["cluster.x-k8s.io/cluster-name"]
	if !success {
		logger.Error("ClusterName label not found in ConfigMap", zap.String("configmap", req.String()))

		return ctrl.Result{}, fmt.Errorf("clusterName %w: %s", ErrValueNotFoundInConfigMap, req.String())
	}

	secretName, success := ccmConfigMap.Data["secretName"]
	if !success {
		logger.Error("SecretName not found in ConfigMap", zap.String("configmap", req.String()))

		return ctrl.Result{}, fmt.Errorf("secretName %w: %s", ErrValueNotFoundInConfigMap, req.String())
	}

	ccmConfig := &cloudControllerManagerConfig{
		Client: r.Client,
		DownstreamClientConfig: DownstreamClientConfig{
			Client:      r.Client,
			ClusterName: clusterName,
		},
		SecretName: secretName,
	}

	downstreamClient, err := ccmConfig.FetchDownstreamKubernetesClient(ctx)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: RequeueAfter,
		}, fmt.Errorf("failed to fetch downstream Kubernetes client: %w", err)
	}

	err = ccmConfig.copySecret(ctx, downstreamClient)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: RequeueAfter,
		}, fmt.Errorf("failed to copy CloudControllerManager secret: %w", err)
	}

	logger.Info("Successfully reconciled CloudControllerManager for ConfigMap", zap.String("configmap", req.String()))

	return ctrl.Result{}, nil
}
