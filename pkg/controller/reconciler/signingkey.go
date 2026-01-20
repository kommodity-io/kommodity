package reconciler

import (
	"context"
	"fmt"
	"strings"

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

func deleteOnlyPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(_ event.UpdateEvent) bool {
			return false
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return true
		},
	}
}

// SetupWithManager sets up the reconciler with the provided manager.
func (r *SigningKeyReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, opt controller.Options) error {
	// Register field indexer for secret type to enable filtering by type in List calls
	err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.Secret{}, "type",
		func(obj client.Object) []string {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				return nil
			}

			return []string{string(secret.Type)}
		})
	if err != nil {
		return fmt.Errorf("failed to create field indexer for secret type: %w", err)
	}

	builder := ctrl.NewControllerManagedBy(mgr).
		Named(SigningKeyControllerName).
		For(&corev1.Secret{}).
		WithOptions(opt).
		WithEventFilter(predicate.And(
			deleteOnlyPredicate(),
			predicates.ResourceNotPausedAndHasFilterLabel(
				mgr.GetScheme(),
				zapr.NewLogger(logging.FromContext(ctx)),
				SigningKeyControllerName,
			),
		))

	err = builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	return nil
}

// Reconcile handles the deletion of the signing key secret.
// When the signing key secret is deleted, it:
// 1. Regenerates a new signing key
// 2. Finds all service account token secrets
// 3. Deletes and recreates them to trigger token regeneration with the new key.
func (r *SigningKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Info("Signing key secret was deleted, regenerating key and rotating tokens",
		zap.String("secret", req.String()))

	// Regenerate the signing key secret
	_, err := r.GetOrCreateSigningKey(ctx, r.CoreV1Client)
	if err != nil {
		logger.Error("Failed to regenerate signing key", zap.Error(err))

		return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfter},
			fmt.Errorf("failed to regenerate signing key: %w", err)
	}

	logger.Info("Successfully regenerated signing key secret")

	secretList := &corev1.SecretList{}

	err = r.List(ctx, secretList,
		client.MatchingFields{"type": string(corev1.SecretTypeServiceAccountToken)},
	)
	if err != nil {
		logger.Error("Failed to list secrets", zap.Error(err))

		return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfter},
			fmt.Errorf("failed to list secrets: %w", err)
	}

	// Process each service account token secret
	for i := range secretList.Items {
		secret := &secretList.Items[i]

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
	}

	logger.Info("Successfully rotated all service account token secrets")

	return ctrl.Result{}, nil
}

// rotateServiceAccountTokenSecret deletes and recreates a service account token secret.
// This triggers the TokensController to generate a new token with the current signing key.
func (r *SigningKeyReconciler) rotateServiceAccountTokenSecret(ctx context.Context, oldSecret *corev1.Secret) error {
	logger := logging.FromContext(ctx)

	saName, ok := oldSecret.Annotations[serviceAccountNameAnnotation]
	if !ok {
		return fmt.Errorf("%w: secret: %s, annotation: %s",
			ErrSecretMissingAnnotation, oldSecret.Name, serviceAccountNameAnnotation)
	}

	labels := make(map[string]string)

	if clusterName, ok := oldSecret.Labels[clusterNameLabel]; ok {
		labels[clusterNameLabel] = clusterName
	}

	if managedBy, ok := oldSecret.Labels[managedByLabel]; ok {
		labels[managedByLabel] = managedBy
	}

	annotations := make(map[string]string)
	annotations[serviceAccountNameAnnotation] = saName

	for k, v := range oldSecret.Annotations {
		if strings.HasPrefix(k, "meta.helm.sh") {
			annotations[k] = v
		}
	}

	err := r.Delete(ctx, oldSecret)
	if err != nil {
		return fmt.Errorf("failed to delete old secret %s: %w", oldSecret.Name, err)
	}

	logger.Info("Deleted old service account token secret",
		zap.String("secret", oldSecret.Name))

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
		zap.String("serviceAccount", saName))

	return nil
}
