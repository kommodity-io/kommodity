package controllers_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
	"github.com/kommodity-io/kommodity/pkg/libkapi/controllers"
)

// TestResolveTokenSecretsNamespace_DefaultsToKubeSystem verifies that
// ResolveTokenSecretsNamespace returns "kube-system" when unset.
func TestResolveTokenSecretsNamespace_DefaultsToKubeSystem(t *testing.T) {
	t.Parallel()

	keyPersistence := &auth.KeyPersistenceConfig{}
	assert.Equal(t, "kube-system", controllers.ResolveTokenSecretsNamespace(keyPersistence))
}

// TestResolveTokenSecretsNamespace_UsesConfigured verifies that
// ResolveTokenSecretsNamespace respects a custom TokenSecretsNamespace.
func TestResolveTokenSecretsNamespace_UsesConfigured(t *testing.T) {
	t.Parallel()

	keyPersistence := &auth.KeyPersistenceConfig{TokenSecretsNamespace: "custom-ns"}
	assert.Equal(t, "custom-ns", controllers.ResolveTokenSecretsNamespace(keyPersistence))
}

// TestHandleSecretUpdate_KeyDataChanged_TriggersRotation verifies that
// HandleSecretUpdate triggers token rotation when the signing key data
// in the Secret changes.
func TestHandleSecretUpdate_KeyDataChanged_TriggersRotation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create an SA token secret in kube-system that should be rotated.
	saTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-1",
			Namespace: "kube-system",
			Annotations: map[string]string{
				controllers.ServiceAccountNameAnnotation: "my-sa",
			},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "test",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	client := fake.NewSimpleClientset(saTokenSecret)
	coreClient := client.CoreV1()

	signingKeyNamespace := auth.DefaultSigningKeyNamespace
	signingKeySecretName := auth.DefaultSigningKeySecretName

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: signingKeySecretName, Namespace: signingKeyNamespace},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("old-key")},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: signingKeySecretName, Namespace: signingKeyNamespace},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("new-key")},
	}

	keyPersistence := &auth.KeyPersistenceConfig{}

	controllers.HandleSecretUpdate(
		oldSecret, newSecret, coreClient, keyPersistence, "kube-system",
		signingKeyNamespace, signingKeySecretName, slog.Default())

	// The SA token secret should have been deleted and recreated.
	got, err := coreClient.Secrets("kube-system").Get(ctx, "sa-token-1", metav1.GetOptions{})
	require.NoError(t, err, "secret should still exist (recreated)")

	// Labels and annotations should be preserved.
	assert.Equal(t, "my-sa", got.Annotations[controllers.ServiceAccountNameAnnotation])
	assert.Equal(t, "test", got.Labels["app.kubernetes.io/managed-by"])
}

// TestHandleSecretUpdate_KeyDataUnchanged_NoRotation verifies that
// HandleSecretUpdate does NOT trigger rotation when the key data hasn't
// changed.
func TestHandleSecretUpdate_KeyDataUnchanged_NoRotation(t *testing.T) {
	t.Parallel()

	saTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-1",
			Namespace: "kube-system",
			Annotations: map[string]string{
				controllers.ServiceAccountNameAnnotation: "my-sa",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{"token": []byte("original-token")},
	}

	client := fake.NewSimpleClientset(saTokenSecret)
	coreClient := client.CoreV1()

	signingKeyNamespace := auth.DefaultSigningKeyNamespace
	signingKeySecretName := auth.DefaultSigningKeySecretName

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: signingKeySecretName, Namespace: signingKeyNamespace},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("same-key")},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: signingKeySecretName, Namespace: signingKeyNamespace},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("same-key")},
	}

	controllers.HandleSecretUpdate(
		oldSecret, newSecret, coreClient, &auth.KeyPersistenceConfig{}, "kube-system",
		signingKeyNamespace, signingKeySecretName, slog.Default())

	// The SA token secret should NOT have been rotated — its data should
	// still contain the original token.
	got, err := coreClient.Secrets("kube-system").Get(context.Background(), "sa-token-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("original-token"), got.Data["token"], "secret should not have been rotated")
}

// TestHandleSecretUpdate_DifferentSecret_NoRotation verifies that
// HandleSecretUpdate ignores updates to Secrets that are not the signing
// key Secret.
func TestHandleSecretUpdate_DifferentSecret_NoRotation(t *testing.T) {
	t.Parallel()

	saTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-1",
			Namespace: "kube-system",
			Annotations: map[string]string{
				controllers.ServiceAccountNameAnnotation: "my-sa",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{"token": []byte("original-token")},
	}

	client := fake.NewSimpleClientset(saTokenSecret)
	coreClient := client.CoreV1()

	// Update a different secret entirely.
	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-other-secret", Namespace: "default"},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("old")},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-other-secret", Namespace: "default"},
		Data:       map[string][]byte{controllers.SigningKeyDataKey: []byte("new")},
	}

	controllers.HandleSecretUpdate(
		oldSecret, newSecret, coreClient, &auth.KeyPersistenceConfig{}, "kube-system",
		auth.DefaultSigningKeyNamespace, auth.DefaultSigningKeySecretName, slog.Default())

	// SA token secret should not have been touched.
	got, err := coreClient.Secrets("kube-system").Get(context.Background(), "sa-token-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("original-token"), got.Data["token"])
}

// TestHandleSecretUpdate_NonSecretType_NoOp verifies that HandleSecretUpdate
// doesn't panic when called with non-Secret objects.
func TestHandleSecretUpdate_NonSecretType_NoOp(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	coreClient := client.CoreV1()

	// Pass non-Secret objects — should be a no-op, not a panic.
	controllers.HandleSecretUpdate(
		"not-a-secret", "also-not-a-secret", coreClient, &auth.KeyPersistenceConfig{}, "kube-system",
		auth.DefaultSigningKeyNamespace, auth.DefaultSigningKeySecretName, slog.Default())
}

// TestRotateServiceAccountTokenSecret_CallsOnTokenRotated verifies that
// the OnTokenRotated callback is called when a token secret is rotated.
func TestRotateServiceAccountTokenSecret_CallsOnTokenRotated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-1",
			Namespace: "kube-system",
			Annotations: map[string]string{
				controllers.ServiceAccountNameAnnotation: "my-sa",
			},
			Labels: map[string]string{"app": "test"},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	client := fake.NewSimpleClientset(oldSecret)
	coreClient := client.CoreV1()

	var rotatedSecret *corev1.Secret

	keyPersistence := &auth.KeyPersistenceConfig{
		OnTokenRotated: func(_ context.Context, secret *corev1.Secret) error {
			rotatedSecret = secret

			return nil
		},
	}

	err := controllers.RotateServiceAccountTokenSecret(
		ctx, coreClient, oldSecret, keyPersistence, slog.Default())
	require.NoError(t, err)

	require.NotNil(t, rotatedSecret, "OnTokenRotated should have been called")
	assert.Equal(t, "sa-token-1", rotatedSecret.Name)
	assert.Equal(t, "my-sa", rotatedSecret.Annotations[controllers.ServiceAccountNameAnnotation])
}

// TestRotateServiceAccountTokenSecret_MissingAnnotation_Skips verifies
// that a SA token secret without the service-account-name annotation is
// skipped (no error, no rotation).
func TestRotateServiceAccountTokenSecret_MissingAnnotation_Skips(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-1",
			Namespace: "kube-system",
			// Missing ServiceAccountNameAnnotation.
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{"token": []byte("original")},
	}

	client := fake.NewSimpleClientset(oldSecret)
	coreClient := client.CoreV1()

	callbackCalled := false

	keyPersistence := &auth.KeyPersistenceConfig{
		OnTokenRotated: func(_ context.Context, _ *corev1.Secret) error {
			callbackCalled = true

			return nil
		},
	}

	err := controllers.RotateServiceAccountTokenSecret(
		ctx, coreClient, oldSecret, keyPersistence, slog.Default())
	require.NoError(t, err, "missing annotation should not return an error")
	assert.False(t, callbackCalled, "OnTokenRotated should not have been called")

	// Original secret should still exist.
	got, err := coreClient.Secrets("kube-system").Get(ctx, "sa-token-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), got.Data["token"], "secret should not have been deleted")
}

// TestRotateTokens_SkipsNonSATokenSecrets verifies that RotateTokens only
// rotates secrets of type SecretTypeServiceAccountToken.
func TestRotateTokens_SkipsNonSATokenSecrets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	saTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token",
			Namespace: "kube-system",
			Annotations: map[string]string{
				controllers.ServiceAccountNameAnnotation: "my-sa",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-secret",
			Namespace: "kube-system",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"data": []byte("should-not-change")},
	}

	client := fake.NewSimpleClientset(saTokenSecret, otherSecret)
	coreClient := client.CoreV1()

	keyPersistence := &auth.KeyPersistenceConfig{}
	controllers.RotateTokens(ctx, coreClient, keyPersistence, "kube-system", slog.Default())

	// The Opaque secret should be untouched.
	got, err := coreClient.Secrets("kube-system").Get(ctx, "other-secret", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("should-not-change"), got.Data["data"],
		"non-SA-token secret should not be rotated")
}

// TestRotateTokens_NoSATokenSecrets_NoOp verifies that RotateTokens is a
// no-op when there are no SA token secrets.
func TestRotateTokens_NoSATokenSecrets_NoOp(t *testing.T) {
	t.Parallel()

	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-secret",
			Namespace: "kube-system",
		},
		Type: corev1.SecretTypeOpaque,
	}

	client := fake.NewSimpleClientset(otherSecret)

	keyPersistence := &auth.KeyPersistenceConfig{}
	controllers.RotateTokens(
		context.Background(), client.CoreV1(), keyPersistence, "kube-system", slog.Default())

	// No error, no panic — the test passes if we get here.
}
