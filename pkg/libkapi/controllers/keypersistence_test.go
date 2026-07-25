package controllers_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// testSigningKey generates a small RSA key for tests (fast generation).
// Using 2048 bits instead of 4096 to keep tests quick.
func testSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	return key
}

// TestCreateSigningKeySecret_CreatesSecret verifies that CreateSigningKeySecret
// creates the signing key Secret when it doesn't already exist.
func TestCreateSigningKeySecret_CreatesSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	signingKey := testSigningKey(t)

	client := fake.NewSimpleClientset()

	err := auth.CreateSigningKeySecret(ctx, client.CoreV1(), signingKey, "test-ns", "test-key")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "test-key", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.SecretTypeOpaque, secret.Type)
	assert.NotEmpty(t, secret.Data["key"])
}

// TestCreateSigningKeySecret_NoOpIfSecretExists verifies that creating a
// signing key Secret when one already exists returns an IsAlreadyExists
// error — which the hook treats as a no-op.
func TestCreateSigningKeySecret_NoOpIfSecretExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	signingKey := testSigningKey(t)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-key",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{"key": []byte("existing-key-data")},
	}

	client := fake.NewSimpleClientset(existingSecret)

	err := auth.CreateSigningKeySecret(ctx, client.CoreV1(), signingKey, "test-ns", "test-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// TestCreateSigningKeySecret_CreatesNamespace verifies that
// CreateSigningKeySecret creates the namespace if it doesn't exist.
func TestCreateSigningKeySecret_CreatesNamespace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	signingKey := testSigningKey(t)

	client := fake.NewSimpleClientset()

	err := auth.CreateSigningKeySecret(ctx, client.CoreV1(), signingKey, "new-ns", "test-key")
	require.NoError(t, err)

	// Namespace should have been created.
	_, err = client.CoreV1().Namespaces().Get(ctx, "new-ns", metav1.GetOptions{})
	require.NoError(t, err)

	// Secret should exist in the new namespace.
	_, err = client.CoreV1().Secrets("new-ns").Get(ctx, "test-key", metav1.GetOptions{})
	require.NoError(t, err)
}

// TestPersistSigningKey_CreatesSecret verifies that PersistSigningKey creates
// the Secret when it doesn't exist (used by the rotation hook on delete).
func TestPersistSigningKey_CreatesSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	signingKey := testSigningKey(t)

	client := fake.NewSimpleClientset()

	err := auth.PersistSigningKey(ctx, client.CoreV1(), signingKey, "test-ns", "test-key")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "test-key", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, secret.Data["key"])
}

// TestPersistSigningKey_OverwritesExisting verifies that PersistSigningKey
// updates the Secret when it already exists (used by the rotation hook
// when the key is regenerated after deletion).
func TestPersistSigningKey_OverwritesExisting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	signingKey := testSigningKey(t)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-key",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{"key": []byte("old-data")},
	}

	client := fake.NewSimpleClientset(existingSecret)

	err := auth.PersistSigningKey(ctx, client.CoreV1(), signingKey, "test-ns", "test-key")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "test-key", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, []byte("old-data"), secret.Data["key"], "key data should have been overwritten")
}
