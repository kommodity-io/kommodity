package reconciler

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// AutoscalerJobConfig defined the temporary config for Autoscaler.
type AutoscalerJobConfig struct {
	Name            string
	Namespace       string
	ChartName       string
	ChartVersion    string
	ChartRepository string
	HasExtraValues  bool
}

// AutoscalerJob struct for managing Autoscaler installation jobs.
type AutoscalerJob struct {
	client.Client

	downstreamClient client.Client
	config           AutoscalerJobConfig
}

// Kubeconfig holds the configuration for accessing the Kommodity cluster from downstream clusters.
type Kubeconfig struct {
	BaseURL   string
	Token     string
	Namespace string
}

//go:embed kubeconfig.tmpl
var kubeconfigTmplFS embed.FS

// PrepareForApply prepares the Autoscaler installation job.
func (a *AutoscalerJob) PrepareForApply(ctx context.Context, cfg *config.KommodityConfig, clusterName string) error {
	logger := logging.FromContext(ctx)
	logger.Info("Preparing Autoscaler Job", zap.String("jobName", a.config.Name))

	var autoscalerSecret corev1.Secret

	err := a.Get(ctx, client.ObjectKey{
		Name:      clusterName + "-cluster-autoscaler",
		Namespace: "default",
	}, &autoscalerSecret)
	if err != nil {
		return fmt.Errorf("failed to get Autoscaler kubeconfig secret: %w", err)
	}

	if len(autoscalerSecret.Data) == 0 {
		return fmt.Errorf("%w kubeconfig: %s", ErrValueNotFoundInSecret, autoscalerSecret.Name)
	}

	kubeconfig := &Kubeconfig{
		BaseURL:   cfg.BaseURL,
		Token:     string(autoscalerSecret.Data["token"]),
		Namespace: string(autoscalerSecret.Data["namespace"]),
	}

	tpl := template.Must(template.New("kubeconfig.tmpl").
		Funcs(sprig.FuncMap()).
		ParseFS(kubeconfigTmplFS, "kubeconfig.tmpl"))

	var buf bytes.Buffer

	err = tpl.Execute(&buf, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to render kubeconfig template: %w", err)
	}

	autoscalerKubeconfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kommodity-cluster-autoscaler-kubeconfig",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			"value": buf.Bytes(),
		},
		Type: corev1.SecretTypeOpaque,
	}

	err = ApplySecretToClient(ctx, a.downstreamClient, autoscalerKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to apply autoscaler kubeconfig secret to downstream cluster: %w", err)
	}

	logger.Info("Successfully prepared Autoscaler Job",
		zap.String("jobName", a.config.Name))

	return nil
}

// Apply applies the Autoscaler installation job to the downstream cluster.
func (a *AutoscalerJob) Apply(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	logger.Info("Applying Autoscaler Job", zap.String("jobName", a.config.Name))

	err := a.applySecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply Autoscaler values secret: %w", err)
	}

	jonConfig := NewHelmInstallConfig(
		a.config.Name,
		a.config.Namespace,
		a.config.ChartName,
		a.config.ChartVersion,
		a.config.ChartRepository,
		a.config.HasExtraValues,
	)

	err = jonConfig.ApplyTemplate(ctx, a.downstreamClient)
	if err != nil {
		return fmt.Errorf("failed to apply Autoscaler Helm install job: %w", err)
	}

	logger.Info("Successfully applied Autoscaler Job",
		zap.String("jobName", a.config.Name))

	return nil
}

func (a *AutoscalerJob) applySecret(ctx context.Context) error {
	a.config.HasExtraValues = true

	var yamlSecret corev1.Secret

	err := a.Get(ctx, client.ObjectKey{
		Name:      "cluster-autoscaler-extra-values",
		Namespace: "default",
	}, &yamlSecret)
	if apierrors.IsNotFound(err) {
		a.config.HasExtraValues = false
	} else if err != nil {
		return fmt.Errorf("failed to get extra values secret: %w", err)
	}

	yamlSecret.Name = a.config.Name + "-extra-values"
	if a.config.Namespace != "" {
		yamlSecret.Namespace = a.config.Namespace
	} else {
		yamlSecret.Namespace = a.config.Name
	}

	err = ApplySecretToClient(ctx, a.downstreamClient, &yamlSecret)
	if err != nil {
		return fmt.Errorf("failed to apply autoscaler secret to downstream cluster: %w", err)
	}

	return nil
}

// AutoscalerReconciler struct for Autoscaler resources.
type AutoscalerReconciler struct {
	client.Client

	cfg *config.KommodityConfig
}

// SetupWithManager sets up the reconciler with the provided manager.
func (r *AutoscalerReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-autoscaler-controller").
		For(&corev1.ConfigMap{}).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
			"kommodity-autoscaler-controller",
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	return nil
}

// Reconcile reconciles Autoscaler resources.
func (r *AutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Reconciling Autoscaler for ConfigMap", zap.String("configmap", req.String()))

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

	errResult, err := r.installAutoscaler(ctx, clusterName, ccmConfigMap.Data)
	if err != nil {
		logger.Error("Failed to install Autoscaler", zap.String("clusterName", clusterName), zap.Error(err))

		return errResult, fmt.Errorf("failed to install Autoscaler for cluster %s: %w", clusterName, err)
	}

	return ctrl.Result{}, nil
}

//nolint:funlen // Primarily fetches values from the ConfigMap with proper error handling.
func (r *AutoscalerReconciler) installAutoscaler(ctx context.Context, clusterName string,
	configMapData map[string]string) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Installing Autoscaler for cluster", zap.String("clusterName", clusterName))

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

	namespace, success := configMapData["namespace"]
	if !success {
		return ctrl.Result{}, fmt.Errorf("namespace %w: cluster %s", ErrValueNotFoundInConfigMap, clusterName)
	}

	url, success := configMapData["repository"]
	if !success {
		return ctrl.Result{}, fmt.Errorf("repository %w", ErrValueNotFoundInConfigMap)
	}

	chartName, success := configMapData["name"]
	if !success {
		return ctrl.Result{}, fmt.Errorf("name %w", ErrValueNotFoundInConfigMap)
	}

	version, success := configMapData["version"]
	if !success {
		version = "latest"
	}

	autoscalerJob := &AutoscalerJob{
		Client:           r.Client,
		downstreamClient: kubeClient,
		config: AutoscalerJobConfig{
			Name:            chartName,
			Namespace:       namespace,
			ChartName:       chartName,
			ChartVersion:    version,
			ChartRepository: url,
		},
	}

	err = autoscalerJob.PrepareForApply(ctx, r.cfg, clusterName)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: RequeueAfter,
		}, fmt.Errorf("failed to prepare Autoscaler Job: %w", err)
	}

	err = autoscalerJob.Apply(ctx)
	if err != nil {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: RequeueAfter,
		}, fmt.Errorf("failed to apply Autoscaler Job: %w", err)
	}

	logger.Info("Successfully installed Autoscaler for cluster",
		zap.String("clusterName", clusterName))

	return ctrl.Result{}, nil
}
