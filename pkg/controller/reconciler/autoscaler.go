package reconciler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

const (
	// AutoscalerConfigMapSuffix is the suffix for the ConfigMap that triggers the Autoscaler installation.
	AutoscalerConfigMapSuffix = "-cluster-autoscaler-config"

	// autoscalerDeploymentName is the name of the Deployment created by the cluster-autoscaler Helm chart.
	autoscalerDeploymentName = "cluster-autoscaler"

	// autoscalerSATokenSecretSuffix is appended to the Cluster name to derive the
	// mgmt-cluster Secret of type kubernetes.io/service-account-token whose `token`
	// field feeds the workload-cluster autoscaler kubeconfig.
	autoscalerSATokenSecretSuffix = "-cluster-autoscaler" //nolint:gosec // secret-name suffix, not a credential

	// autoscalerSecretTokenKey is the data key holding the signed SA JWT in the
	// service-account-token Secret.
	autoscalerSecretTokenKey = "token"

	// autoscalerControllerName is the controller name used to scope the filter
	// label and the controller registration.
	autoscalerControllerName = "kommodity-autoscaler-controller"

	// autoscalerKubeconfigHashAnnotation is stamped on the downstream
	// cluster-autoscaler Deployment's pod template to trigger a rolling restart
	// whenever the mounted kubeconfig's bearer token changes. cluster-autoscaler
	// parses the kubeconfig once at process start, so a Secret-volume update
	// alone is not sufficient to pick up a rotated SA token.
	autoscalerKubeconfigHashAnnotation = "kommodity.io/kubeconfig-hash"
)

//go:embed kubeconfig.tmpl
var kubeconfigTmplFS embed.FS

// PrepareForApply prepares the Autoscaler installation job.
//
//nolint:funlen // fetch token, render kubeconfig, apply to downstream, roll autoscaler on token change.
func (a *AutoscalerJob) PrepareForApply(ctx context.Context, cfg *config.KommodityConfig, clusterName string) error {
	logger := logging.FromContext(ctx)
	logger.Info("Preparing Autoscaler Job", zap.String("jobName", a.config.Name))

	var autoscalerSecret corev1.Secret

	err := a.Get(ctx, client.ObjectKey{
		Name:      clusterName + autoscalerSATokenSecretSuffix,
		Namespace: "default",
	}, &autoscalerSecret)
	if err != nil {
		return fmt.Errorf("failed to get Autoscaler kubeconfig secret: %w", err)
	}

	token := autoscalerSecret.Data[autoscalerSecretTokenKey]
	if len(token) == 0 {
		return fmt.Errorf("%w: %s", ErrTokenNotPopulated, autoscalerSecret.Name)
	}

	namespace := autoscalerSecret.Data["namespace"]
	if len(namespace) == 0 {
		return fmt.Errorf("%w namespace: %s", ErrValueNotFoundInSecret, autoscalerSecret.Name)
	}

	kubeconfig := &Kubeconfig{
		BaseURL:   cfg.BaseURL,
		Token:     string(token),
		Namespace: string(namespace),
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

	err = a.ensureAutoscalerRolloutOnTokenChange(ctx, token)
	if err != nil {
		return fmt.Errorf("failed to ensure autoscaler rollout on token change: %w", err)
	}

	logger.Info("Successfully prepared Autoscaler Job",
		zap.String("jobName", a.config.Name))

	return nil
}

// Apply applies the Autoscaler installation job to the downstream cluster.
func (a *AutoscalerJob) Apply(ctx context.Context, clusterName string) error {
	logger := logging.FromContext(ctx)
	logger.Info("Applying Autoscaler Job", zap.String("jobName", a.config.Name))

	err := a.applySecret(ctx, clusterName)
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

// IsInstalled checks if the autoscaler Deployment already exists in the downstream cluster.
func (a *AutoscalerJob) IsInstalled(ctx context.Context) (bool, error) {
	deployment := &appsv1.Deployment{}

	namespace := a.config.Namespace
	if namespace == "" {
		namespace = a.config.Name
	}

	err := a.downstreamClient.Get(ctx, client.ObjectKey{
		Name:      autoscalerDeploymentName,
		Namespace: namespace,
	}, deployment)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to check autoscaler deployment: %w", err)
	}

	return true, nil
}

// ensureAutoscalerRolloutOnTokenChange triggers a rolling restart of the
// downstream cluster-autoscaler Deployment whenever the bearer token in its
// mounted kubeconfig changes, by stamping a sha256 hash of the token onto the
// pod template annotations. If the Deployment does not yet exist (autoscaler
// not installed) the call is a no-op: the pod will read the current kubeconfig
// on first start.
func (a *AutoscalerJob) ensureAutoscalerRolloutOnTokenChange(ctx context.Context, token []byte) error {
	logger := logging.FromContext(ctx)

	tokenHashSum := sha256.Sum256(token)
	tokenHash := hex.EncodeToString(tokenHashSum[:])

	namespace := a.config.Namespace
	if namespace == "" {
		namespace = a.config.Name
	}

	deployment := &appsv1.Deployment{}
	deploymentKey := client.ObjectKey{
		Name:      autoscalerDeploymentName,
		Namespace: namespace,
	}

	err := a.downstreamClient.Get(ctx, deploymentKey, deployment)
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to get cluster-autoscaler Deployment %s/%s: %w",
			namespace, autoscalerDeploymentName, err)
	}

	if deployment.Spec.Template.Annotations[autoscalerKubeconfigHashAnnotation] == tokenHash {
		return nil
	}

	patched := deployment.DeepCopy()
	if patched.Spec.Template.Annotations == nil {
		patched.Spec.Template.Annotations = map[string]string{}
	}

	patched.Spec.Template.Annotations[autoscalerKubeconfigHashAnnotation] = tokenHash

	err = a.downstreamClient.Patch(ctx, patched, client.MergeFrom(deployment))
	if err != nil {
		return fmt.Errorf("failed to patch cluster-autoscaler Deployment %s/%s with kubeconfig hash: %w",
			namespace, autoscalerDeploymentName, err)
	}

	logger.Info("Rolled cluster-autoscaler Deployment after kubeconfig token change",
		zap.String("namespace", namespace),
		zap.String("deployment", autoscalerDeploymentName))

	return nil
}

func (a *AutoscalerJob) applySecret(ctx context.Context, clusterName string) error {
	a.config.HasExtraValues = true

	var yamlSecret corev1.Secret

	err := a.Get(ctx, client.ObjectKey{
		Name:      clusterName + "-cluster-autoscaler-extra-values",
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
//
// The controller's primary watch is the autoscaler ConfigMap (label-filtered).
// It also watches mgmt-cluster service-account-token Secrets so that whenever
// the signed JWT is rotated (e.g. by the signing-key controller) we re-render
// the workload-cluster kubeconfig secret automatically.
func (r *AutoscalerReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	configMapPredicate := predicates.ResourceNotPausedAndHasFilterLabel(
		mgr.GetScheme(),
		zapr.NewLogger(logging.FromContext(ctx)),
		autoscalerControllerName,
	)

	builder := ctrl.NewControllerManagedBy(mgr).
		Named(autoscalerControllerName).
		For(&corev1.ConfigMap{}, ctrlbuilder.WithPredicates(configMapPredicate)).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.configMapForAutoscalerTokenSecret),
			ctrlbuilder.WithPredicates(autoscalerTokenSecretPredicate()),
		).
		WithOptions(opt)

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

	result, err := r.installAutoscaler(ctx, clusterName, ccmConfigMap.Data)
	if err != nil {
		logger.Error("Failed to install Autoscaler", zap.String("clusterName", clusterName), zap.Error(err))

		return result, fmt.Errorf("failed to install Autoscaler for cluster %s: %w", clusterName, err)
	}

	return result, nil
}

//nolint:funlen,cyclop // Handles ConfigMap values, validates downstream cluster readiness, and installs the autoscaler.
func (r *AutoscalerReconciler) installAutoscaler(ctx context.Context, clusterName string,
	configMapData map[string]string) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Installing Autoscaler for cluster", zap.String("clusterName", clusterName))

	kubeClient, err := (&DownstreamClientConfig{
		Client:      r.Client,
		ClusterName: clusterName,
	}).FetchDownstreamKubernetesClient(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Cluster kubeconfig not ready yet, requeuing",
				zap.String("clusterName", clusterName),
				zap.Duration("requeueAfter", RequeueAfter))

			return ctrl.Result{RequeueAfter: RequeueAfter}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to fetch kubeconfig from secret: %w", err)
	}

	err = CheckClusterReady(ctx, kubeClient)
	if err != nil {
		logger.Info("Downstream cluster not ready yet, requeuing",
			zap.String("clusterName", clusterName),
			zap.Duration("requeueAfter", RequeueAfter))

		//nolint:nilerr // intentionally return nil to avoid exponential backoff
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
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
		if apierrors.IsNotFound(err) || errors.Is(err, ErrTokenNotPopulated) {
			logger.Info("Autoscaler secret not ready yet, requeuing",
				zap.String("clusterName", clusterName),
				zap.Duration("requeueAfter", RequeueAfter))

			return ctrl.Result{RequeueAfter: RequeueAfter}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to prepare Autoscaler Job: %w", err)
	}

	installed, err := autoscalerJob.IsInstalled(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if autoscaler is installed: %w", err)
	}

	if installed {
		logger.Info("Autoscaler already installed, skipping installation",
			zap.String("clusterName", clusterName))

		return ctrl.Result{}, nil
	}

	err = autoscalerJob.Apply(ctx, clusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply Autoscaler Job: %w", err)
	}

	logger.Info("Successfully installed Autoscaler for cluster",
		zap.String("clusterName", clusterName))

	return ctrl.Result{}, nil
}

// configMapForAutoscalerTokenSecret maps an SA-token Secret event to a reconcile
// request for the autoscaler ConfigMap belonging to the same Cluster. The map
// uses the secret's `cluster.x-k8s.io/cluster-name` label plus the well-known
// secret-name suffix to identify the owning Cluster.
func (r *AutoscalerReconciler) configMapForAutoscalerTokenSecret(
	_ context.Context,
	obj client.Object,
) []reconcile.Request {
	secret, success := obj.(*corev1.Secret)
	if !success {
		return nil
	}

	clusterName, success := secret.Labels[clusterNameLabel]
	if !success {
		return nil
	}

	if secret.Name != clusterName+autoscalerSATokenSecretSuffix {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: secret.Namespace,
			Name:      clusterName + AutoscalerConfigMapSuffix,
		},
	}}
}

// autoscalerTokenSecretPredicate filters Secret events to service-account-token
// Secrets that back a cluster autoscaler, and only forwards events where the
// `token` value actually changed. This avoids spurious reconciles on unrelated
// Secret writes and ensures every kubeconfig refresh corresponds to a real JWT
// rotation.
func autoscalerTokenSecretPredicate() predicate.Predicate {
	asAutoscalerTokenSecret := func(obj client.Object) (*corev1.Secret, bool) {
		secret, success := obj.(*corev1.Secret)
		if !success {
			return nil, false
		}

		if secret.Type != corev1.SecretTypeServiceAccountToken {
			return nil, false
		}

		clusterName, success := secret.Labels[clusterNameLabel]
		if !success {
			return nil, false
		}

		return secret, secret.Name == clusterName+autoscalerSATokenSecretSuffix
	}

	return predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			secret, matched := asAutoscalerTokenSecret(createEvent.Object)
			if !matched {
				return false
			}

			return len(secret.Data[autoscalerSecretTokenKey]) > 0
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			newSecret, matched := asAutoscalerTokenSecret(updateEvent.ObjectNew)
			if !matched {
				return false
			}

			oldSecret, success := updateEvent.ObjectOld.(*corev1.Secret)
			if !success {
				return false
			}

			return !bytes.Equal(
				oldSecret.Data[autoscalerSecretTokenKey],
				newSecret.Data[autoscalerSecretTokenKey],
			)
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return false
		},
	}
}
