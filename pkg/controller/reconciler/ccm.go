package reconciler

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/yaml"
)

const (
	requeueAfter = 10 * time.Second
)

//go:embed secret.tmpl
var tmplFS embed.FS

type cloudControllerManagerConfig struct {
	client.Client

	ClusterName string
	SecretName  string
}

func (c *cloudControllerManagerConfig) fetchDownstreamKubernetesClient(ctx context.Context) (client.Client, error) {
	kubeConfigSecret := &corev1.Secret{}

	err := c.Get(ctx, client.ObjectKey{
		Name:      c.ClusterName + "-kubeconfig",
		Namespace: "default",
	}, kubeConfigSecret)
	if err != nil {
		logging.FromContext(ctx).Error("Failed to get kubeconfig secret",
			zap.String("secretName", c.SecretName),
			zap.Error(err))

		return nil, fmt.Errorf("failed to get kubeconfig secret %s: %w", c.SecretName, err)
	}

	kubeConfigBytes, ok := kubeConfigSecret.Data["value"]
	if !ok {
		logging.FromContext(ctx).Error("Kubeconfig value not found in secret", zap.String("secretName", c.SecretName))

		return nil, fmt.Errorf("kubeconfig %w: %s", ErrValueNotFoundInSecret, c.SecretName)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		logging.FromContext(ctx).Error("Failed to create REST config from kubeconfig", zap.Error(err))

		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	downstreamClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		logging.FromContext(ctx).Error("Failed to create downstream Kubernetes client", zap.Error(err))

		return nil, fmt.Errorf("failed to create downstream Kubernetes client: %w", err)
	}

	return downstreamClient, nil
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

	funcs := sprig.FuncMap()
	funcs["b64encBytes"] = func(b []byte) string {
		return base64.StdEncoding.EncodeToString(b)
	}

	tpl := template.Must(template.New("secret.tmpl").
		Funcs(funcs).
		ParseFS(tmplFS, "secret.tmpl"))

	var buf bytes.Buffer

	err = tpl.Execute(&buf, providerSecret)
	if err != nil {
		return fmt.Errorf("failed to render secret template: %w", err)
	}

	var renderedSecret corev1.Secret

	err = yaml.Unmarshal(buf.Bytes(), &renderedSecret)
	if err != nil {
		return fmt.Errorf("failed to decode rendered secret: %w", err)
	}

	err = downstreamClient.Create(ctx, &renderedSecret)
	if err != nil {
		return fmt.Errorf("failed to create provider secret %s: %w", c.SecretName, err)
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
		Client:      r.Client,
		ClusterName: clusterName,
		SecretName:  secretName,
	}

	downstreamClient, err := ccmConfig.fetchDownstreamKubernetesClient(ctx)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: requeueAfter,
		}, fmt.Errorf("failed to fetch downstream Kubernetes client: %w", err)
	}

	err = ccmConfig.copySecret(ctx, downstreamClient)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: requeueAfter,
		}, fmt.Errorf("failed to copy CloudControllerManager secret: %w", err)
	}

	logger.Info("Successfully reconciled CloudControllerManager for ConfigMap", zap.String("configmap", req.String()))

	return ctrl.Result{}, nil
}
