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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

const (
	// AnnotationAutoscalerSASecretName carries the name of the management-cluster
	// ServiceAccount-token Secret whose token is rendered into the kubeconfig the
	// downstream cluster-autoscaler uses to call back into CAPI.
	//nolint:gosec // annotation key, not a credential
	AnnotationAutoscalerSASecretName = "kommodity.io/autoscaler-sa-secret-name"

	// autoscalerKubeconfigDownstreamName is the downstream Secret name expected by
	// the cluster-autoscaler chart (via `clusterAPIKubeconfigSecret`).
	autoscalerKubeconfigDownstreamName = "kommodity-cluster-autoscaler-kubeconfig"

	// autoscalerKubeconfigDownstreamNamespace is where cluster-autoscaler runs and
	// looks up its CAPI-access kubeconfig.
	autoscalerKubeconfigDownstreamNamespace = "kube-system"

	// autoscalerKubeconfigDownstreamKey is the data key the cluster-autoscaler
	// chart mounts (via `clusterAPICloudConfigPath: /etc/kubernetes/value`).
	autoscalerKubeconfigDownstreamKey = "value"

	// autoscalerPayloadSecretSuffix names the CRS-payload Secret stamped by this
	// reconciler in the management cluster.
	//nolint:gosec // suffix, not a credential
	autoscalerPayloadSecretSuffix = "-autoscaler-kubeconfig"

	// autoscalerPayloadDataKey is the key inside the CRS-payload Secret. CAPI
	// applies each value as a manifest to the workload cluster.
	autoscalerPayloadDataKey = "kubeconfig.yaml"
)

//go:embed kubeconfig.tmpl
var autoscalerKubeconfigTmplFS embed.FS

// autoscalerKubeconfig is the data model for kubeconfig.tmpl.
type autoscalerKubeconfig struct {
	BaseURL   string
	Token     string
	Namespace string
}

// AutoscalerCRSReconciler watches CAPI Cluster objects and, when the autoscaler
// annotation is present, stamps a ClusterResourceSet payload Secret holding the
// downstream kubeconfig Secret manifest that the cluster-autoscaler chart needs
// to authenticate back to the management cluster.
type AutoscalerCRSReconciler struct {
	client.Client

	cfg *config.KommodityConfig
}

// NewAutoscalerCRSReconciler constructs the reconciler with its dependencies.
func NewAutoscalerCRSReconciler(c client.Client, cfg *config.KommodityConfig) *AutoscalerCRSReconciler {
	return &AutoscalerCRSReconciler{Client: c, cfg: cfg}
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AutoscalerCRSReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	logger := logging.FromContext(ctx)
	logger.Info("Setting up Autoscaler ClusterResourceSet payload reconciler")

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-autoscaler-crs-controller").
		For(&clusterv1.Cluster{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.clustersForSourceSecret),
		).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPaused(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up Autoscaler CRS controller with manager: %w", err)
	}

	return nil
}

// Reconcile builds and applies the CRS payload Secret for the given Cluster.
func (r *AutoscalerCRSReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	cluster := &clusterv1.Cluster{}

	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get Cluster %s: %w", req.String(), err)
	}

	sourceSecretName, hasAnnotation := cluster.Annotations[AnnotationAutoscalerSASecretName]
	if !hasAnnotation {
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling autoscaler CRS payload Secret",
		zap.String("cluster", req.String()),
		zap.String("saSecret", sourceSecretName))

	token, namespace, requeue, err := r.fetchSAToken(ctx, cluster.Namespace, sourceSecretName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
	}

	kubeconfigBytes, err := r.renderKubeconfig(token, namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to render autoscaler kubeconfig: %w", err)
	}

	payloadManifest, err := buildAutoscalerDownstreamSecret(kubeconfigBytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build autoscaler downstream secret manifest: %w", err)
	}

	err = r.applyAutoscalerPayload(ctx, cluster, payloadManifest)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply autoscaler CRS payload Secret: %w", err)
	}

	logger.Info("Successfully reconciled autoscaler CRS payload Secret",
		zap.String("cluster", req.String()))

	return ctrl.Result{}, nil
}

// clustersForSourceSecret enqueues Clusters whose autoscaler-sa-secret-name
// annotation matches the changed Secret. Lets SA-token rotation propagate
// downstream.
func (r *AutoscalerCRSReconciler) clustersForSourceSecret(
	ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	return clustersByAnnotationValue(ctx, r.Client, secret, AnnotationAutoscalerSASecretName)
}

func (r *AutoscalerCRSReconciler) fetchSAToken(ctx context.Context,
	namespace string, name string) (string, string, bool, error) {
	logger := logging.FromContext(ctx)
	saSecret := &corev1.Secret{}

	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, saSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Autoscaler SA Secret not found yet, requeuing",
				zap.String("namespace", namespace),
				zap.String("name", name))

			return "", "", true, nil
		}

		return "", "", false, fmt.Errorf("failed to get autoscaler SA Secret %s/%s: %w",
			namespace, name, err)
	}

	token := saSecret.Data["token"]
	if len(token) == 0 {
		logger.Info("Autoscaler SA token not populated yet, requeuing",
			zap.String("secret", saSecret.Namespace+"/"+saSecret.Name))

		return "", "", true, nil
	}

	saNamespace := saSecret.Data["namespace"]
	if len(saNamespace) == 0 {
		return "", "", false, fmt.Errorf("%w namespace: %s/%s",
			ErrValueNotFoundInSecret, saSecret.Namespace, saSecret.Name)
	}

	return string(token), string(saNamespace), false, nil
}

func (r *AutoscalerCRSReconciler) renderKubeconfig(token string, namespace string) ([]byte, error) {
	tpl, err := template.New("kubeconfig.tmpl").
		Funcs(sprig.FuncMap()).
		ParseFS(autoscalerKubeconfigTmplFS, "kubeconfig.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig template: %w", err)
	}

	var buf bytes.Buffer

	err = tpl.Execute(&buf, &autoscalerKubeconfig{
		BaseURL:   r.cfg.BaseURL,
		Token:     token,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute kubeconfig template: %w", err)
	}

	return buf.Bytes(), nil
}

func buildAutoscalerDownstreamSecret(kubeconfig []byte) ([]byte, error) {
	downstream := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalerKubeconfigDownstreamName,
			Namespace: autoscalerKubeconfigDownstreamNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			autoscalerKubeconfigDownstreamKey: kubeconfig,
		},
	}

	manifest, err := yaml.Marshal(downstream)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal autoscaler downstream secret manifest: %w", err)
	}

	return manifest, nil
}

func (r *AutoscalerCRSReconciler) applyAutoscalerPayload(ctx context.Context,
	cluster *clusterv1.Cluster, manifest []byte) error {
	return upsertCRSPayloadSecret(ctx, r.Client, cluster,
		cluster.Name+autoscalerPayloadSecretSuffix, autoscalerPayloadDataKey, manifest)
}
