package libkapi_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	certutil "k8s.io/client-go/util/cert"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestLoadOrCreateWebhookCert_SecretExists_LoadsCert verifies that
// LoadOrCreateWebhookCert loads the existing certificate from a Secret when
// it already exists, rather than generating a new one.
func TestLoadOrCreateWebhookCert_SecretExists_LoadsCert(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	originalCertPEM, originalKeyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      libkapi.DefaultWebhookCertSecretName,
			Namespace: "libkapi",
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       originalCertPEM,
			corev1.TLSPrivateKeyKey: originalKeyPEM,
		},
		Type: corev1.SecretTypeTLS,
	}

	client := fake.NewSimpleClientset(existingSecret)

	loadedCertPEM, loadedKeyPEM, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)

	assert.Equal(t, originalCertPEM, loadedCertPEM)
	assert.Equal(t, originalKeyPEM, loadedKeyPEM)
}

// TestLoadOrCreateWebhookCert_SecretDoesNotExist_CreatesCert verifies that
// LoadOrCreateWebhookCert generates a new certificate and creates the
// Secret when it doesn't exist.
func TestLoadOrCreateWebhookCert_SecretDoesNotExist_CreatesCert(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	createdCertPEM, createdKeyPEM, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)
	assert.NotEmpty(t, createdCertPEM)
	assert.NotEmpty(t, createdKeyPEM)

	secret, err := client.CoreV1().Secrets("libkapi").Get(ctx, libkapi.DefaultWebhookCertSecretName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
	assert.Equal(t, createdCertPEM, secret.Data[corev1.TLSCertKey])
	assert.Equal(t, createdKeyPEM, secret.Data[corev1.TLSPrivateKeyKey])
	assert.Equal(t, auth.SigningKeyValue, secret.Labels[auth.SigningKeyLabel])
}

// TestLoadOrCreateWebhookCert_IsIdempotent verifies that calling
// LoadOrCreateWebhookCert twice returns the same certificate (the one from
// the Secret created on the first call).
func TestLoadOrCreateWebhookCert_IsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	cert1, key1, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)

	cert2, key2, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)

	assert.Equal(t, cert1, cert2, "second call should return the same certificate as the first")
	assert.Equal(t, key1, key2, "second call should return the same key as the first")
}

// TestLoadOrCreateWebhookCert_EmptyDNSNames_ReturnsError verifies that
// generating a certificate with no DNS names returns an error instead of
// panicking on an out-of-range index into an empty slice.
func TestLoadOrCreateWebhookCert_EmptyDNSNames_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	_, _, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), nil, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.Error(t, err)
	assert.ErrorIs(t, err, libkapi.ErrWebhookCertDNSNamesRequired)
}

// TestLoadWebhookCertFromSecret_MissingData_ReturnsError verifies that a
// Secret existing but missing tls.crt/tls.key data returns
// ErrWebhookCertDataMissing.
func TestLoadWebhookCertFromSecret_MissingData_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      libkapi.DefaultWebhookCertSecretName,
			Namespace: "libkapi",
		},
		Data: map[string][]byte{},
		Type: corev1.SecretTypeTLS,
	}

	client := fake.NewSimpleClientset(existingSecret)

	_, _, err := libkapi.LoadWebhookCertFromSecret(ctx, client.CoreV1(), "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.Error(t, err)
	assert.ErrorIs(t, err, libkapi.ErrWebhookCertDataMissing)
}

// TestCreateWebhookCertSecret_CreatesSecret verifies that
// CreateWebhookCertSecret creates a kubernetes.io/tls Secret with both the
// certificate and key data.
func TestCreateWebhookCertSecret_CreatesSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	client := fake.NewSimpleClientset()

	err = libkapi.CreateWebhookCertSecret(ctx, client.CoreV1(), certPEM, keyPEM, "test-ns", "test-secret")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "test-secret", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
	assert.Equal(t, certPEM, secret.Data[corev1.TLSCertKey])
	assert.Equal(t, keyPEM, secret.Data[corev1.TLSPrivateKeyKey])
	assert.Equal(t, auth.SigningKeyValue, secret.Labels[auth.SigningKeyLabel])
}

// TestCreateWebhookCertSecret_AlreadyExists_ReturnsError verifies that
// CreateWebhookCertSecret returns an IsAlreadyExists error (not silently
// overwriting) when the Secret already exists - the create-or-adopt
// pattern relies on this to detect a race with another replica.
func TestCreateWebhookCertSecret_AlreadyExists_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "test-ns"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
		Type: corev1.SecretTypeTLS,
	}

	client := fake.NewSimpleClientset(existingSecret)

	err = libkapi.CreateWebhookCertSecret(ctx, client.CoreV1(), certPEM, keyPEM, "test-ns", "test-secret")
	require.Error(t, err)
	assert.True(t, apierrors.IsAlreadyExists(err), "expected an IsAlreadyExists error, got: %v", err)
}
