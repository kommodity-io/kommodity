package reconciler

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonsv1 "sigs.k8s.io/cluster-api/api/addons/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/yaml"
)

const (
	// AnnotationCCMSecretName carries the name of the management-cluster Secret that
	// holds the credentials for the cloud controller manager (typically a kubeconfig
	// for kubevirt, API keys for scaleway).
	AnnotationCCMSecretName = "kommodity.io/ccm-secret-name" //nolint:gosec // annotation key, not a credential

	// AnnotationCCMDownstreamSecretName carries the name the wrapped credentials
	// Secret should take inside the workload cluster.
	//nolint:gosec // annotation key, not a credential
	AnnotationCCMDownstreamSecretName = "kommodity.io/ccm-downstream-secret-name"

	// downstreamCCMSecretNamespace is the workload-cluster namespace where the
	// CCM credentials Secret is delivered. CCM Deployments run in kube-system.
	downstreamCCMSecretNamespace = "kube-system"

	// ccmPayloadSecretSuffix is appended to the Cluster name to derive the name
	// of the CRS-payload Secret that wraps the credentials.
	ccmPayloadSecretSuffix = "-ccm-secret"

	// ccmPayloadDataKey is the key inside the CRS-payload Secret's data map. CAPI
	// applies each value as a Kubernetes manifest in the workload cluster.
	ccmPayloadDataKey = "credentials.yaml"
)

// CCMCRSReconciler watches CAPI Cluster objects and, when CCM is enabled via
// annotations, stamps a ClusterResourceSet payload Secret that wraps the
// management-side credentials Secret as a workload-cluster Secret manifest.
type CCMCRSReconciler struct {
	client.Client
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *CCMCRSReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	logger := logging.FromContext(ctx)
	logger.Info("Setting up CCM ClusterResourceSet payload reconciler")

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-ccm-crs-controller").
		For(&clusterv1.Cluster{}).
		WithOptions(opt)

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up CCM CRS controller with manager: %w", err)
	}

	return nil
}

// Reconcile builds and applies the CRS payload Secret for the given Cluster.
func (r *CCMCRSReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	cluster := &clusterv1.Cluster{}

	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get Cluster %s: %w", req.String(), err)
	}

	sourceSecretName, downstreamSecretName, skip, err := resolveCCMAnnotations(cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if skip {
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling CCM CRS payload Secret",
		zap.String("cluster", req.String()),
		zap.String("sourceSecret", sourceSecretName),
		zap.String("downstreamSecret", downstreamSecretName))

	sourceSecret, requeue, err := r.fetchSourceSecret(ctx, cluster.Namespace, sourceSecretName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
	}

	payloadManifest, err := buildDownstreamSecretManifest(downstreamSecretName, sourceSecret.Data)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build downstream secret manifest: %w", err)
	}

	err = r.applyPayloadSecret(ctx, cluster, payloadManifest)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply CCM CRS payload Secret: %w", err)
	}

	logger.Info("Successfully reconciled CCM CRS payload Secret",
		zap.String("cluster", req.String()))

	return ctrl.Result{}, nil
}

func resolveCCMAnnotations(cluster *clusterv1.Cluster) (string, string, bool, error) {
	sourceName, hasSource := cluster.Annotations[AnnotationCCMSecretName]
	if !hasSource {
		return "", "", true, nil
	}

	downstreamName, hasDownstream := cluster.Annotations[AnnotationCCMDownstreamSecretName]
	if !hasDownstream {
		return "", "", false, fmt.Errorf("%w: %s on %s/%s",
			ErrClusterMissingAnnotation, AnnotationCCMDownstreamSecretName,
			cluster.Namespace, cluster.Name)
	}

	return sourceName, downstreamName, false, nil
}

func (r *CCMCRSReconciler) fetchSourceSecret(ctx context.Context,
	namespace string, name string) (*corev1.Secret, bool, error) {
	logger := logging.FromContext(ctx)
	sourceSecret := &corev1.Secret{}

	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sourceSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Source CCM Secret not found yet, requeuing",
				zap.String("namespace", namespace),
				zap.String("name", name))

			return nil, true, nil
		}

		return nil, false, fmt.Errorf("failed to get source CCM Secret %s/%s: %w",
			namespace, name, err)
	}

	return sourceSecret, false, nil
}

func buildDownstreamSecretManifest(name string, data map[string][]byte) ([]byte, error) {
	downstream := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: downstreamCCMSecretNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	manifest, err := yaml.Marshal(downstream)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal downstream secret manifest: %w", err)
	}

	return manifest, nil
}

func (r *CCMCRSReconciler) applyPayloadSecret(ctx context.Context,
	cluster *clusterv1.Cluster, manifest []byte) error {
	payloadName := cluster.Name + ccmPayloadSecretSuffix

	payload := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      payloadName,
			Namespace: cluster.Namespace,
		},
	}

	operation, err := ctrl.CreateOrUpdate(ctx, r.Client, payload, func() error {
		payload.Type = addonsv1.ClusterResourceSetSecretType
		payload.Data = map[string][]byte{
			ccmPayloadDataKey: manifest,
		}

		if payload.Labels == nil {
			payload.Labels = map[string]string{}
		}

		payload.Labels["cluster.x-k8s.io/cluster-name"] = cluster.Name
		payload.Labels["app.kubernetes.io/managed-by"] = "kommodity"

		payload.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
			Name:       cluster.Name,
			UID:        cluster.UID,
		}}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to upsert CCM CRS payload Secret %s/%s: %w",
			cluster.Namespace, payloadName, err)
	}

	logging.FromContext(ctx).Info("CCM CRS payload Secret operation",
		zap.String("operation", string(operation)),
		zap.String("secret", cluster.Namespace+"/"+payloadName))

	return nil
}
