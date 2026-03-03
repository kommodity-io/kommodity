package reconciler

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// SigningKeyControllerName is the name of the signing key controller.
	SigningKeyControllerName = "kommodity-signing-key-controller"
	// SigningKeyDataKey is the key in the secret data that stores the private key PEM.
	SigningKeyDataKey = "key"
	// SigningKeyUpdatedAnnotation is the annotation key used to indicate when the signing key was last updated.
	SigningKeyUpdatedAnnotation = "kommodity.io/signing-key-updated"

	// serviceAccountNameAnnotation is the annotation key for the service account name.
	serviceAccountNameAnnotation = "kubernetes.io/service-account.name"
	// clusterNameLabel is the label key for the cluster name.
	clusterNameLabel = "cluster.x-k8s.io/cluster-name"
	// managedByLabel is the label key for the managed-by annotation.
	managedByLabel = "app.kubernetes.io/managed-by"
)

// SigningKeyReconciler reconciles the service account signing key secret.
// When the signing key secret is deleted, it regenerates the key and
// recreates all service account token secrets to use the new key.
type SigningKeyReconciler struct {
	client.Client

	CoreV1Client corev1client.CoreV1Interface

	// GetOrCreateSigningKey retrieves or generates the signing key.
	// This is injected to avoid circular dependencies with the server package.
	GetOrCreateSigningKey func(ctx context.Context, client corev1client.CoreV1Interface) (any, error)
}

func deleteOrUpdatePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			oldSecret, success := updateEvent.ObjectOld.(*corev1.Secret)
			if !success {
				return false
			}

			newSecret, success := updateEvent.ObjectNew.(*corev1.Secret)
			if !success {
				return false
			}

			// Only trigger if the key data actually changed
			return !bytes.Equal(
				oldSecret.Data[SigningKeyDataKey],
				newSecret.Data[SigningKeyDataKey])
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return true
		},
	}
}

// SetupWithManager sets up the reconciler with the provided manager.
func (r *SigningKeyReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named(SigningKeyControllerName).
		For(&corev1.Secret{}).
		WithOptions(opt).
		WithEventFilter(predicate.And(
			deleteOrUpdatePredicate(),
			predicates.ResourceNotPausedAndHasFilterLabel(
				mgr.GetScheme(),
				zapr.NewLogger(logging.FromContext(ctx)),
				SigningKeyControllerName,
			),
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	return nil
}

// Reconcile handles the deletion of the signing key secret.
// When the signing key secret is deleted, it:
// 1. Fetch / regenerates a new signing key
// 2. Finds all service account token secrets
// 3. Deletes and recreates them to trigger token regeneration with the new key.
func (r *SigningKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Signing key secret was deleted, regenerating key and rotating tokens",
		zap.String("secret", req.String()))

	_, err := r.GetOrCreateSigningKey(ctx, r.CoreV1Client)
	if err != nil {
		logger.Error("Failed to fetch or regenerate signing key", zap.Error(err))

		return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfter},
			fmt.Errorf("failed to fetch or regenerate signing key: %w", err)
	}

	logger.Info("Successfully fetched or regenerated signing key secret")

	secretList := &corev1.SecretList{}

	err = r.List(ctx, secretList, client.InNamespace("kube-system"))
	if err != nil {
		logger.Error("Failed to list secrets", zap.Error(err))

		return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfter},
			fmt.Errorf("failed to list secrets: %w", err)
	}

	// Process each service account token secret (filter client-side since
	// Kubernetes API server doesn't support field selectors on Secret.Type)
	for i := range secretList.Items {
		secret := &secretList.Items[i]

		if secret.Type != corev1.SecretTypeServiceAccountToken {
			continue
		}

		saName, ok := secret.Annotations[serviceAccountNameAnnotation]
		if !ok {
			logger.Warn("Service account token secret missing required annotation, skipping",
				zap.String("secret", secret.Name),
				zap.String("annotation", serviceAccountNameAnnotation))

			continue
		}

		logger.Info("Rotating service account token secret",
			zap.String("secret", secret.Name),
			zap.String("serviceAccount", saName))

		err := r.rotateServiceAccountTokenSecret(ctx, secret)
		if err != nil {
			logger.Error("Failed to rotate service account token secret",
				zap.String("secret", secret.Name),
				zap.Error(err))

			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfter},
				fmt.Errorf("failed to rotate service account token secret %s: %w", secret.Name, err)
		}

		logger.Info("Successfully rotated service account token secret",
			zap.String("secret", secret.Name),
			zap.String("serviceAccount", saName))
	}

	logger.Info("Successfully rotated all service account token secrets")

	return ctrl.Result{}, nil
}

// extractSecretMetadata extracts the cluster name, labels, and annotations from the old secret.
func extractSecretMetadata(oldSecret *corev1.Secret) (string, map[string]string, map[string]string, error) {
	saName, exists := oldSecret.Annotations[serviceAccountNameAnnotation]
	if !exists {
		return "", nil, nil, fmt.Errorf("%w: secret: %s, annotation: %s",
			ErrSecretMissingAnnotation, oldSecret.Name, serviceAccountNameAnnotation)
	}

	clusterName, exists := oldSecret.Labels[clusterNameLabel]
	if !exists {
		return "", nil, nil, fmt.Errorf("%w: secret: %s, label: %s",
			ErrSecretMissingLabel, oldSecret.Name, clusterNameLabel)
	}

	labels := map[string]string{clusterNameLabel: clusterName}
	if managedBy, found := oldSecret.Labels[managedByLabel]; found {
		labels[managedByLabel] = managedBy
	}

	annotations := map[string]string{serviceAccountNameAnnotation: saName}

	for k, v := range oldSecret.Annotations {
		if strings.HasPrefix(k, "meta.helm.sh") {
			annotations[k] = v
		}
	}

	return clusterName, labels, annotations, nil
}

// rotateServiceAccountTokenSecret deletes and recreates a service account token secret.
// This triggers the TokensController to generate a new token with the current signing key.
func (r *SigningKeyReconciler) rotateServiceAccountTokenSecret(
	ctx context.Context,
	oldSecret *corev1.Secret,
) error {
	logger := logging.FromContext(ctx)

	clusterName, labels, annotations, err := extractSecretMetadata(oldSecret)
	if err != nil {
		return err
	}

	err = r.Delete(ctx, oldSecret)
	if err != nil {
		return fmt.Errorf("failed to delete old secret %s: %w", oldSecret.Name, err)
	}

	logger.Info("Deleted old service account token secret", zap.String("secret", oldSecret.Name))

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        oldSecret.Name,
			Namespace:   oldSecret.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	err = r.Create(ctx, newSecret)
	if err != nil {
		return fmt.Errorf("failed to create new secret %s: %w", oldSecret.Name, err)
	}

	logger.Info("Created new service account token secret",
		zap.String("secret", newSecret.Name),
		zap.String("serviceAccount", annotations[serviceAccountNameAnnotation]))

	err = r.updateAutoscalerConfigMap(ctx, clusterName, oldSecret.Namespace)
	if err != nil {
		logger.Warn("Failed to update autoscaler ConfigMap with signing key timestamp",
			zap.String("clusterName", clusterName),
			zap.Error(err))
	}

	return nil
}

// updateAutoscalerConfigMap fetches the cluster autoscaler ConfigMap and adds
// the SigningKeyUpdatedAnnotation with the current unix timestamp.
func (r *SigningKeyReconciler) updateAutoscalerConfigMap(
	ctx context.Context,
	clusterName string,
	namespace string,
) error {
	configMapName := clusterName + AutoscalerConfigMapSuffix

	configMap := &corev1.ConfigMap{}
	configMapKey := client.ObjectKey{
		Namespace: namespace,
		Name:      configMapName,
	}

	err := r.Get(ctx, configMapKey, configMap)
	if err != nil {
		return fmt.Errorf("failed to get autoscaler ConfigMap %s: %w", configMapName, err)
	}

	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}

	configMap.Annotations[SigningKeyUpdatedAnnotation] = strconv.FormatInt(time.Now().Unix(), 10)

	err = r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to update autoscaler ConfigMap %s: %w", configMapName, err)
	}

	return nil
}
