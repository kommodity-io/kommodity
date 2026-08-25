package libkapi_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	certutil "k8s.io/client-go/util/cert"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
)

// farOutsideRenewalWindow and wellWithinRenewalWindow are chosen relative
// to certutil.GenerateSelfSignedCertKey's fixed 365-day validity - large
// enough margins that exact clock timing (validFrom is set 1h in the past
// to avoid clock-skew flakes) never affects which side of "due" a test
// lands on.
const (
	wellWithinRenewalWindow = 1 * time.Hour
	farOutsideRenewalWindow = 400 * 24 * time.Hour
)

// errSimulatedConflict is the static error injected into the fake
// clientset's "update" reactor to simulate another replica winning the
// rotation race.
var errSimulatedConflict = errors.New("simulated conflict")

// TestRenewWebhookCertIfDue_NotDue_ReturnsExistingUnchanged verifies that a
// freshly created certificate, nowhere near its renewal window, is
// returned as-is with no Secret mutation.
func TestRenewWebhookCertIfDue_NotDue_ReturnsExistingUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	originalCertPEM, originalKeyPEM, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)

	certPEM, keyPEM, err := libkapi.RenewWebhookCertIfDue(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName,
		wellWithinRenewalWindow, time.Now)
	require.NoError(t, err)

	assert.Equal(t, originalCertPEM, certPEM)
	assert.Equal(t, originalKeyPEM, keyPEM)
}

// TestRenewWebhookCertIfDue_Due_RotatesAndUpdatesSecret verifies that a
// certificate within its renewal window is rotated: a new certificate is
// generated and the Secret is updated to match.
func TestRenewWebhookCertIfDue_Due_RotatesAndUpdatesSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	originalCertPEM, _, err := libkapi.LoadOrCreateWebhookCert(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName)
	require.NoError(t, err)

	rotatedCertPEM, rotatedKeyPEM, err := libkapi.RenewWebhookCertIfDue(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName,
		farOutsideRenewalWindow, time.Now)
	require.NoError(t, err)

	assert.NotEqual(t, originalCertPEM, rotatedCertPEM, "expected a due certificate to be rotated")

	secret, err := client.CoreV1().Secrets("libkapi").Get(ctx, libkapi.DefaultWebhookCertSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, rotatedCertPEM, secret.Data[corev1.TLSCertKey])
	assert.Equal(t, rotatedKeyPEM, secret.Data[corev1.TLSPrivateKeyKey])
}

// TestRenewWebhookCertIfDue_SecretMissing_CreatesIt verifies that
// RenewWebhookCertIfDue delegates to LoadOrCreateWebhookCert when the
// Secret doesn't exist yet.
func TestRenewWebhookCertIfDue_SecretMissing_CreatesIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	client := fake.NewSimpleClientset()

	certPEM, keyPEM, err := libkapi.RenewWebhookCertIfDue(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName,
		wellWithinRenewalWindow, time.Now)
	require.NoError(t, err)
	assert.NotEmpty(t, certPEM)
	assert.NotEmpty(t, keyPEM)

	_, err = client.CoreV1().Secrets("libkapi").Get(ctx, libkapi.DefaultWebhookCertSecretName, metav1.GetOptions{})
	require.NoError(t, err, "expected the Secret to have been created")
}

// TestRenewWebhookCertIfDue_ConflictOnUpdate_AdoptsWinner verifies that
// when another replica wins the rotation race (Update returns a conflict),
// RenewWebhookCertIfDue re-reads and returns the winner's data instead of
// erroring or retrying - no leader coordination needed, the next periodic
// tick re-checks.
func TestRenewWebhookCertIfDue_ConflictOnUpdate_AdoptsWinner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	winnerCertPEM, winnerKeyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: libkapi.DefaultWebhookCertSecretName, Namespace: "libkapi"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       winnerCertPEM,
			corev1.TLSPrivateKeyKey: winnerKeyPEM,
		},
		Type: corev1.SecretTypeTLS,
	}

	client := fake.NewSimpleClientset(existingSecret)

	client.PrependReactor("update", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		conflictErr := apierrors.NewConflict(
			corev1.Resource("secrets"), libkapi.DefaultWebhookCertSecretName, errSimulatedConflict)

		return true, nil, conflictErr
	})

	certPEM, keyPEM, err := libkapi.RenewWebhookCertIfDue(
		ctx, client.CoreV1(), []string{"localhost"}, "libkapi", libkapi.DefaultWebhookCertSecretName,
		farOutsideRenewalWindow, time.Now)
	require.NoError(t, err)

	assert.Equal(t, winnerCertPEM, certPEM, "expected the winner's certificate, not a newly-generated one")
	assert.Equal(t, winnerKeyPEM, keyPEM)

	secret, err := client.CoreV1().Secrets("libkapi").Get(ctx, libkapi.DefaultWebhookCertSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, winnerCertPEM, secret.Data[corev1.TLSCertKey], "the rejected update must not have landed")
}
