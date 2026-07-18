package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestGenerateRSAPrivateKey_ReturnsValidKey verifies that the key generator
// produces a non-nil RSA key of the expected size.
func TestGenerateRSAPrivateKey_ReturnsValidKey(t *testing.T) {
	t.Parallel()

	key, err := auth.GenerateRSAPrivateKey()
	require.NoError(t, err)
	require.NotNil(t, key)

	assert.Equal(t, auth.RSAKeySize, key.N.BitLen(), "expected %d-bit key", auth.RSAKeySize)
}

// TestConvertRSAKeyToPEM_RoundTrip verifies that converting a key to PEM
// and back produces the same key.
func TestConvertRSAKeyToPEM_RoundTrip(t *testing.T) {
	t.Parallel()

	original, err := auth.GenerateRSAPrivateKey()
	require.NoError(t, err)

	pemBytes := auth.ConvertRSAKeyToPEM(original)
	assert.NotEmpty(t, pemBytes)

	restored, err := auth.ConvertPEMToRSAKey(pemBytes)
	require.NoError(t, err)

	assert.Equal(t, 0, original.D.Cmp(restored.D), "private exponent should match")
	assert.Equal(t, original.E, restored.E, "public exponent should match")
}

// TestConvertPEMToRSAKey_InvalidPEM_ReturnsError verifies that garbage input
// to the PEM converter returns ErrPEMDecodeFailed.
func TestConvertPEMToRSAKey_InvalidPEM_ReturnsError(t *testing.T) {
	t.Parallel()

	_, err := auth.ConvertPEMToRSAKey([]byte("not a PEM block"))
	require.ErrorIs(t, err, auth.ErrPEMDecodeFailed)
}

// TestLoadOrCreateSigningKey_SecretExists_LoadsKey verifies that
// LoadOrCreateSigningKey loads the existing key from a Secret when it
// already exists, rather than generating a new one.
func TestLoadOrCreateSigningKey_SecretExists_LoadsKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	originalKey, err := auth.GenerateRSAPrivateKey()
	require.NoError(t, err)

	keyPEM := auth.ConvertRSAKeyToPEM(originalKey)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-signing-key",
			Namespace: "kommodity-system",
		},
		Data: map[string][]byte{
			auth.SigningKeyDataKey: keyPEM,
		},
	}

	client := fake.NewSimpleClientset(existingSecret)

	// Namespace must exist for the LoadOrCreate call to proceed.
	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "kommodity-system"},
	}, metav1.CreateOptions{})
	if err != nil {
		require.Contains(t, err.Error(), "already exists")
	}

	keyPersistence := &auth.KeyPersistenceConfig{
		Namespace:  "kommodity-system",
		SecretName: "sa-signing-key",
	}

	loadedKey, err := auth.LoadOrCreateSigningKey(ctx, client.CoreV1(), keyPersistence)
	require.NoError(t, err)
	require.NotNil(t, loadedKey)

	// The loaded key should match the original.
	assert.Equal(t, 0, originalKey.D.Cmp(loadedKey.D), "loaded key should match original")
}

// TestLoadOrCreateSigningKey_SecretDoesNotExist_CreatesKey verifies that
// LoadOrCreateSigningKey generates a new key and creates the Secret when
// it doesn't exist.
func TestLoadOrCreateSigningKey_SecretDoesNotExist_CreatesKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	keyPersistence := &auth.KeyPersistenceConfig{
		Namespace:  "kommodity-system",
		SecretName: "sa-signing-key",
	}

	createdKey, err := auth.LoadOrCreateSigningKey(ctx, client.CoreV1(), keyPersistence)
	require.NoError(t, err)
	require.NotNil(t, createdKey)

	// The Secret should now exist with the key data.
	secret, err := client.CoreV1().Secrets("kommodity-system").Get(ctx, "sa-signing-key", metav1.GetOptions{})
	require.NoError(t, err)

	keyPEM, ok := secret.Data[auth.SigningKeyDataKey]
	require.True(t, ok, "secret should contain key data")
	assert.NotEmpty(t, keyPEM)

	// The persisted key should match the returned key.
	persistedKey, err := auth.ConvertPEMToRSAKey(keyPEM)
	require.NoError(t, err)
	assert.Equal(t, 0, createdKey.D.Cmp(persistedKey.D), "persisted key should match returned key")
}

// TestLoadOrCreateSigningKey_IsIdempotent verifies that calling
// LoadOrCreateSigningKey twice returns the same key (the one from the
// Secret created on the first call).
func TestLoadOrCreateSigningKey_IsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	keyPersistence := &auth.KeyPersistenceConfig{
		Namespace:  "kommodity-system",
		SecretName: "sa-signing-key",
	}

	key1, err := auth.LoadOrCreateSigningKey(ctx, client.CoreV1(), keyPersistence)
	require.NoError(t, err)

	key2, err := auth.LoadOrCreateSigningKey(ctx, client.CoreV1(), keyPersistence)
	require.NoError(t, err)

	assert.Equal(t, 0, key1.D.Cmp(key2.D), "second call should return the same key as the first")
}

// TestLoadOrCreateSigningKey_MissingKeyData_ReturnsError verifies that
// when the Secret exists but is missing the key data, LoadOrCreateSigningKey
// returns an error (rather than silently generating a new key).
func TestLoadOrCreateSigningKey_MissingKeyData_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-signing-key",
			Namespace: "kommodity-system",
		},
		Data: map[string][]byte{},
	}

	client := fake.NewSimpleClientset(existingSecret)

	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "kommodity-system"},
	}, metav1.CreateOptions{})
	if err != nil {
		require.Contains(t, err.Error(), "already exists")
	}

	keyPersistence := &auth.KeyPersistenceConfig{
		Namespace:  "kommodity-system",
		SecretName: "sa-signing-key",
	}

	_, err = auth.LoadOrCreateSigningKey(ctx, client.CoreV1(), keyPersistence)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrSigningKeyDataMissing)
}

// TestCreateSigningKeySecret_CreatesSecret verifies that
// CreateSigningKeySecret creates the Secret with the key data.
func TestCreateSigningKeySecret_CreatesSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	key, err := auth.GenerateRSAPrivateKey()
	require.NoError(t, err)

	client := fake.NewSimpleClientset()

	err = auth.CreateSigningKeySecret(ctx, client.CoreV1(), key, "test-ns", "test-secret")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "test-secret", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, corev1.SecretTypeOpaque, secret.Type)
	assert.NotEmpty(t, secret.Data[auth.SigningKeyDataKey])
	assert.Equal(t, auth.SigningKeyValue, secret.Labels[auth.SigningKeyLabel])
}

// TestEnsureNamespace_CreatesIfMissing verifies that EnsureNamespace creates
// the namespace when it doesn't exist.
func TestEnsureNamespace_CreatesIfMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := fake.NewSimpleClientset()

	err := auth.EnsureNamespace(ctx, client.CoreV1(), "new-namespace")
	require.NoError(t, err)

	_, err = client.CoreV1().Namespaces().Get(ctx, "new-namespace", metav1.GetOptions{})
	require.NoError(t, err)
}

// TestEnsureNamespace_NoOpIfExists verifies that EnsureNamespace is a
// no-op when the namespace already exists.
func TestEnsureNamespace_NoOpIfExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	existingNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-ns"},
	}

	client := fake.NewSimpleClientset(existingNs)

	err := auth.EnsureNamespace(ctx, client.CoreV1(), "existing-ns")
	require.NoError(t, err)
}
