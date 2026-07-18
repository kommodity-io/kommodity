package controllers

import (
	"bytes"
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

const (
	// DefaultTokenSecretsNamespace is the default namespace where SA token
	// secrets are listed for rotation.
	DefaultTokenSecretsNamespace = "kube-system"
	// SigningKeyDataKey matches auth.SigningKeyDataKey.
	SigningKeyDataKey = "key"
	// ServiceAccountNameAnnotation is the annotation for the SA name in token secrets.
	ServiceAccountNameAnnotation = "kubernetes.io/service-account.name"
)

// SigningKeyRotationHookConfig holds the inputs needed to build the signing
// key rotation post-start hook.
type SigningKeyRotationHookConfig struct {
	// KeyPersistence is the persistence config (namespace, secret name,
	// token secrets namespace, OnTokenRotated callback).
	KeyPersistence *auth.KeyPersistenceConfig

	// SigningKey is the current in-memory signing key. Used to persist
	// when the Secret is deleted (regenerate + persist).
	SigningKey *rsa.PrivateKey
}

// NewSigningKeyRotationHook builds a PostStartHookFunc that registers a
// watch on the signing key Secret via the Secret informer. When the key
// data changes or the Secret is deleted, it regenerates/persists the key
// and rotates all SA token secrets.
//
// The informer factory must be started by another hook (e.g. the token
// controller hook). This hook only registers the event handler.
func NewSigningKeyRotationHook(
	hookCfg SigningKeyRotationHookConfig,
	loopbackConfig *restclient.Config,
	informerFactory informers.SharedInformerFactory,
	logger *slog.Logger,
) (genericapiserver.PostStartHookFunc, error) {
	kubeClient, err := kubernetes.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client for signing key rotation: %w", err)
	}

	secretInformer := informerFactory.Core().V1().Secrets().Informer()

	SetupSigningKeyRotation(
		secretInformer, &auth.ServiceAccountConfig{
			KeyPersistence: hookCfg.KeyPersistence,
		},
		hookCfg.SigningKey,
		kubeClient.CoreV1(),
		logger,
	)

	return func(_ genericapiserver.PostStartHookContext) error {
		return nil
	}, nil
}

// SetupSigningKeyRotation registers an event handler on the Secret informer
// that watches the signing key Secret. When the key data changes or the
// Secret is deleted, it regenerates/persists the key and rotates all SA
// token secrets.
func SetupSigningKeyRotation(
	secretInformer cache.SharedInformer,
	saCfg *auth.ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	coreClient corev1client.CoreV1Interface,
	logger *slog.Logger,
) {
	keyPersistence := saCfg.KeyPersistence
	tokenSecretsNamespace := ResolveTokenSecretsNamespace(keyPersistence)
	signingKeyNamespace := auth.ResolveSigningKeyNamespace(keyPersistence)
	signingKeySecretName := auth.ResolveSigningKeySecretName(keyPersistence)

	_, _ = secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			HandleSecretUpdate(oldObj, newObj, coreClient, keyPersistence,
				tokenSecretsNamespace, signingKeyNamespace, signingKeySecretName, logger)
		},
		DeleteFunc: func(obj any) {
			HandleSecretDelete(obj, coreClient, keyPersistence,
				tokenSecretsNamespace, signingKeyNamespace, signingKeySecretName, logger, signingKey)
		},
	})
}

// ResolveTokenSecretsNamespace returns the namespace for SA token secrets,
// defaulting to "kube-system" if not set.
func ResolveTokenSecretsNamespace(kp *auth.KeyPersistenceConfig) string {
	if kp.TokenSecretsNamespace != "" {
		return kp.TokenSecretsNamespace
	}

	return DefaultTokenSecretsNamespace
}

// HandleSecretUpdate checks if the signing key Secret's key data changed
// and triggers token rotation if so.
func HandleSecretUpdate(
	oldObj, newObj any,
	coreClient corev1client.CoreV1Interface,
	keyPersistence *auth.KeyPersistenceConfig,
	tokenSecretsNamespace string,
	signingKeyNamespace string,
	signingKeySecretName string,
	logger *slog.Logger,
) {
	oldSecret, ok1 := oldObj.(*corev1.Secret)
	newSecret, ok2 := newObj.(*corev1.Secret)

	if !ok1 || !ok2 {
		return
	}

	if newSecret.Namespace != signingKeyNamespace || newSecret.Name != signingKeySecretName {
		return
	}

	// Only trigger if the key data actually changed.
	if bytes.Equal(oldSecret.Data[SigningKeyDataKey], newSecret.Data[SigningKeyDataKey]) {
		return
	}

	RotateTokens(context.Background(), coreClient, keyPersistence, tokenSecretsNamespace, logger)
}

// HandleSecretDelete regenerates the signing key, persists it, and rotates
// tokens when the signing key Secret is deleted.
func HandleSecretDelete(
	obj any,
	coreClient corev1client.CoreV1Interface,
	keyPersistence *auth.KeyPersistenceConfig,
	tokenSecretsNamespace string,
	signingKeyNamespace string,
	signingKeySecretName string,
	logger *slog.Logger,
	signingKey *rsa.PrivateKey,
) {
	deletedSecret, ok := obj.(*corev1.Secret)
	if !ok {
		return
	}

	if deletedSecret.Namespace != signingKeyNamespace || deletedSecret.Name != signingKeySecretName {
		return
	}

	ctx := context.Background()

	err := auth.PersistSigningKey(ctx, coreClient, signingKey,
		signingKeyNamespace, signingKeySecretName)
	if err != nil {
		logger.Error("Failed to persist regenerated signing key", "error", err)

		return
	}

	RotateTokens(ctx, coreClient, keyPersistence, tokenSecretsNamespace, logger)
}

// RotateTokens lists all SA token secrets in the configured namespace,
// deletes and recreates each one (triggering the token controller to issue
// new tokens), and calls OnTokenRotated for each rotated secret.
func RotateTokens(
	ctx context.Context,
	coreClient corev1client.CoreV1Interface,
	keyPersistence *auth.KeyPersistenceConfig,
	tokenSecretsNamespace string,
	logger *slog.Logger,
) {
	secretList, err := coreClient.Secrets(tokenSecretsNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error("Failed to list secrets for rotation",
			"namespace", tokenSecretsNamespace, "error", err)

		return
	}

	for i := range secretList.Items {
		secret := &secretList.Items[i]

		if secret.Type != corev1.SecretTypeServiceAccountToken {
			continue
		}

		err := RotateServiceAccountTokenSecret(ctx, coreClient, secret, keyPersistence, logger)
		if err != nil {
			logger.Error("Failed to rotate service account token secret",
				"secret", secret.Name, "error", err)
		}
	}
}

// RotateServiceAccountTokenSecret deletes and recreates a SA token secret,
// preserving its labels and annotations.
func RotateServiceAccountTokenSecret(
	ctx context.Context,
	coreClient corev1client.CoreV1Interface,
	oldSecret *corev1.Secret,
	keyPersistence *auth.KeyPersistenceConfig,
	logger *slog.Logger,
) error {
	saName, ok := oldSecret.Annotations[ServiceAccountNameAnnotation]
	if !ok {
		logger.Warn("Service account token secret missing required annotation, skipping",
			"secret", oldSecret.Name,
			"annotation", ServiceAccountNameAnnotation)

		return nil
	}

	labels := maps.Clone(oldSecret.Labels)
	annotations := maps.Clone(oldSecret.Annotations)

	err := coreClient.Secrets(oldSecret.Namespace).Delete(ctx, oldSecret.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete old secret %s: %w", oldSecret.Name, err)
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        oldSecret.Name,
			Namespace:   oldSecret.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	_, err = coreClient.Secrets(oldSecret.Namespace).Create(ctx, newSecret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create new secret %s: %w", oldSecret.Name, err)
	}

	logger.Info("Rotated service account token secret",
		"secret", newSecret.Name,
		"serviceAccount", saName)

	if keyPersistence.OnTokenRotated != nil {
		err := keyPersistence.OnTokenRotated(ctx, newSecret)
		if err != nil {
			logger.Warn("OnTokenRotated callback failed",
				"secret", newSecret.Name, "error", err)
		}
	}

	return nil
}
