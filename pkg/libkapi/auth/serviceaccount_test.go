package auth_test

import (
	"context"
	"crypto/rsa"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestWithServiceAccount_SetsConfig verifies that WithServiceAccount stores
// the config on the auth config struct (observable through Resolve).
func TestWithServiceAccount_SetsConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithServiceAccount(auth.ServiceAccountConfig{Issuer: "custom-issuer"}),
	}, slog.Default())
	require.NoError(t, err)

	require.NotNil(t, resolved.SAConfig)
	assert.Equal(t, "custom-issuer", resolved.SAConfig.Issuer)
}

// TestResolveSigningKey_ProvidedKey_ReturnsIt verifies that ResolveSigningKey
// returns the caller-provided key without generating.
func TestResolveSigningKey_ProvidedKey_ReturnsIt(t *testing.T) {
	t.Parallel()

	providedKey := &rsa.PrivateKey{} // zero key is fine for this test
	cfg := &auth.ServiceAccountConfig{SigningKey: providedKey}

	key, err := auth.ResolveSigningKey(cfg)
	require.NoError(t, err)
	assert.Same(t, providedKey, key, "should return the provided key")
}

// TestResolveSigningKey_NilKey_GeneratesNewKey verifies that ResolveSigningKey
// generates a new RSA key when SigningKey is nil.
func TestResolveSigningKey_NilKey_GeneratesNewKey(t *testing.T) {
	t.Parallel()

	cfg := &auth.ServiceAccountConfig{SigningKey: nil}

	key, err := auth.ResolveSigningKey(cfg)
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.Equal(t, auth.RSAKeySize, key.N.BitLen(), "expected %d-bit key", auth.RSAKeySize)
}

// TestResolveSigningKey_NilKey_GeneratesDifferentKeys verifies that two
// calls to ResolveSigningKey with nil produce different keys.
func TestResolveSigningKey_NilKey_GeneratesDifferentKeys(t *testing.T) {
	t.Parallel()

	cfg := &auth.ServiceAccountConfig{SigningKey: nil}

	key1, err := auth.ResolveSigningKey(cfg)
	require.NoError(t, err)

	key2, err := auth.ResolveSigningKey(cfg)
	require.NoError(t, err)

	assert.NotEqual(t, 0, key1.D.Cmp(key2.D), "two generated keys should differ")
}

// TestLoopbackSAGetter_GetServiceAccount verifies that LoopbackSAGetter
// fetches a ServiceAccount from the kube client.
func TestLoopbackSAGetter_GetServiceAccount(t *testing.T) {
	t.Parallel()

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "my-sa", Namespace: "default"},
	}

	client := fake.NewSimpleClientset(sa)
	getter := &auth.LoopbackSAGetter{Client: client}

	got, err := getter.GetServiceAccount("default", "my-sa")
	require.NoError(t, err)
	assert.Equal(t, "my-sa", got.Name)
	assert.Equal(t, "default", got.Namespace)
}

// TestLoopbackSAGetter_GetServiceAccount_NotFound verifies that
// LoopbackSAGetter returns an error when the SA doesn't exist.
func TestLoopbackSAGetter_GetServiceAccount_NotFound(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	getter := &auth.LoopbackSAGetter{Client: client}

	_, err := getter.GetServiceAccount("default", "missing-sa")
	require.Error(t, err)
}

// TestLoopbackSAGetter_GetPod_ReturnsNotSupported verifies that GetPod
// returns the "not supported" error.
func TestLoopbackSAGetter_GetPod_ReturnsNotSupported(t *testing.T) {
	t.Parallel()

	getter := &auth.LoopbackSAGetter{Client: fake.NewSimpleClientset()}

	_, err := getter.GetPod("default", "my-pod")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrResourceNotSupported)
}

// TestLoopbackSAGetter_GetNode_ReturnsNotSupported verifies that GetNode
// returns the "not supported" error.
func TestLoopbackSAGetter_GetNode_ReturnsNotSupported(t *testing.T) {
	t.Parallel()

	getter := &auth.LoopbackSAGetter{Client: fake.NewSimpleClientset()}

	_, err := getter.GetNode("my-node")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrResourceNotSupported)
}

// TestLoopbackSAGetter_GetSecret verifies that LoopbackSAGetter fetches
// a Secret from the kube client.
func TestLoopbackSAGetter_GetSecret(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
	}

	client := fake.NewSimpleClientset(secret)
	getter := &auth.LoopbackSAGetter{Client: client}

	got, err := getter.GetSecret("default", "my-secret")
	require.NoError(t, err)
	assert.Equal(t, "my-secret", got.Name)
}

// TestLoopbackSecretsGetter_Secrets verifies that LoopbackSecretsGetter
// returns a non-nil SecretInterface.
func TestLoopbackSecretsGetter_Secrets(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	getter := &auth.LoopbackSecretsGetter{Client: client}

	si := getter.Secrets("default")
	assert.NotNil(t, si)
}

// TestResolveSALookup_BothProvided_UsesProvided verifies that when both
// TokenGetter and SecretsGetter are provided, they are used directly
// (no loopback client is built).
func TestResolveSALookup_BothProvided_UsesProvided(t *testing.T) {
	t.Parallel()

	saCfg := &auth.ServiceAccountConfig{
		TokenGetter:   &auth.LoopbackSAGetter{Client: fake.NewSimpleClientset()},
		SecretsGetter: &auth.LoopbackSecretsGetter{Client: fake.NewSimpleClientset()},
	}

	saGetter, secretsGet, err := auth.ResolveSALookup(saCfg, nil)
	require.NoError(t, err)

	assert.Same(t, saCfg.TokenGetter, saGetter, "should use the provided TokenGetter")
	assert.Same(t, saCfg.SecretsGetter, secretsGet, "should use the provided SecretsGetter")
}
