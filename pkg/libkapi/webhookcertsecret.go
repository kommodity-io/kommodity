package libkapi

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	certutil "k8s.io/client-go/util/cert"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// DefaultWebhookCertSecretName is the name of the Secret that stores the
// webhook serving certificate.
//
//nolint:gosec // G101 — this is a Secret name, not a credential.
const DefaultWebhookCertSecretName = "libkapi-webhook-serving-cert"

// LoadWebhookCertFromSecret loads the webhook serving certificate and key
// from an existing Secret.
func LoadWebhookCertFromSecret(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	namespace string,
	secretName string,
) ([]byte, []byte, error) {
	secret, err := client.Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get webhook cert secret: %w", err)
	}

	certPEM, keyPEM, err := webhookCertDataFromSecret(secret)
	if err != nil {
		return nil, nil, err
	}

	return certPEM, keyPEM, nil
}

// webhookCertDataFromSecret extracts tls.crt/tls.key from a webhook cert
// Secret's data, failing if either is missing.
func webhookCertDataFromSecret(secret *corev1.Secret) ([]byte, []byte, error) {
	certPEM, certOK := secret.Data[corev1.TLSCertKey]
	keyPEM, keyOK := secret.Data[corev1.TLSPrivateKeyKey]

	if !certOK || !keyOK {
		return nil, nil, fmt.Errorf("%w: %q/%q", ErrWebhookCertDataMissing, corev1.TLSCertKey, corev1.TLSPrivateKeyKey)
	}

	return certPEM, keyPEM, nil
}

// CreateWebhookCertSecret creates a new kubernetes.io/tls Secret with the
// webhook serving certificate and key. Returns an IsAlreadyExists error if
// the Secret already exists. The namespace is created if it doesn't exist.
func CreateWebhookCertSecret(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	certPEM []byte,
	keyPEM []byte,
	namespace string,
	secretName string,
) error {
	err := auth.EnsureNamespace(ctx, client, namespace)
	if err != nil {
		return fmt.Errorf("failed to ensure webhook cert secret namespace: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				auth.SigningKeyLabel: auth.SigningKeyValue,
			},
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
		Type: corev1.SecretTypeTLS,
	}

	_, err = client.Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create webhook cert secret: %w", err)
	}

	return nil
}

// LoadOrCreateWebhookCert loads the webhook serving certificate from a
// Kubernetes Secret if it exists, or generates a new self-signed
// certificate and creates the Secret if it doesn't. This ensures the
// certificate persists and is shared across replicas.
//
// Must be called after the server is listening (it uses the loopback client).
func LoadOrCreateWebhookCert(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	dnsNames []string,
	namespace string,
	secretName string,
) ([]byte, []byte, error) {
	certPEM, keyPEM, err := LoadWebhookCertFromSecret(ctx, client, namespace, secretName)
	if err == nil {
		return certPEM, keyPEM, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, nil, fmt.Errorf("failed to load webhook cert from secret: %w", err)
	}

	// Secret doesn't exist — generate a new self-signed certificate.
	newCertPEM, newKeyPEM, err := generateWebhookCert(dnsNames)
	if err != nil {
		return nil, nil, err
	}

	// Create the Secret. If a race caused it to be created between our
	// load and create, fall back to loading the existing certificate.
	err = CreateWebhookCertSecret(ctx, client, newCertPEM, newKeyPEM, namespace, secretName)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return LoadWebhookCertFromSecret(ctx, client, namespace, secretName)
		}

		return nil, nil, err
	}

	return newCertPEM, newKeyPEM, nil
}

// RenewWebhookCertIfDue returns the webhook cert Secret's current
// certificate, or a freshly rotated one if the current one is within
// renewalWindow of expiring. If the Secret doesn't exist yet, it delegates
// to LoadOrCreateWebhookCert.
//
// Rotation uses an optimistic-concurrency Update (carrying the Get's
// ResourceVersion): if another replica wins the race, the resulting
// IsConflict is not retried within this call - the winner's data is
// re-read and returned instead, matching every other replica's own next
// periodic check converging on the same Secret without any leader
// coordination.
func RenewWebhookCertIfDue(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	dnsNames []string,
	namespace string,
	secretName string,
	renewalWindow time.Duration,
	now func() time.Time,
) ([]byte, []byte, error) {
	secret, err := client.Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return LoadOrCreateWebhookCert(ctx, client, dnsNames, namespace, secretName)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get webhook cert secret: %w", err)
	}

	certPEM, keyPEM, err := webhookCertDataFromSecret(secret)
	if err != nil {
		return nil, nil, err
	}

	due, err := certNeedsRenewal(certPEM, renewalWindow, now())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse webhook certificate: %w", err)
	}

	if !due {
		return certPEM, keyPEM, nil
	}

	return rotateWebhookCertSecret(ctx, client, dnsNames, secret)
}

// rotateWebhookCertSecret generates a fresh certificate and writes it into
// secret with an optimistic-concurrency Update, carrying the
// ResourceVersion secret was read at. Losing that race is not an error and
// is not retried: the winning replica's certificate is re-read and returned
// instead, so every replica converges on the same Secret without any leader
// coordination - see RenewWebhookCertIfDue's own doc.
func rotateWebhookCertSecret(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	dnsNames []string,
	secret *corev1.Secret,
) ([]byte, []byte, error) {
	newCertPEM, newKeyPEM, err := generateWebhookCert(dnsNames)
	if err != nil {
		return nil, nil, err
	}

	updated := secret.DeepCopy()
	updated.Data = map[string][]byte{
		corev1.TLSCertKey:       newCertPEM,
		corev1.TLSPrivateKeyKey: newKeyPEM,
	}

	secrets := client.Secrets(secret.Namespace)

	_, err = secrets.Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		if !apierrors.IsConflict(err) {
			return nil, nil, fmt.Errorf("failed to update webhook cert secret: %w", err)
		}

		winner, getErr := secrets.Get(ctx, secret.Name, metav1.GetOptions{})
		if getErr != nil {
			return nil, nil, fmt.Errorf("failed to re-read webhook cert secret after conflict: %w", getErr)
		}

		return webhookCertDataFromSecret(winner)
	}

	return newCertPEM, newKeyPEM, nil
}

// generateWebhookCert generates a self-signed serving certificate valid for
// every name in dnsNames, using the first as the certificate's Common Name.
func generateWebhookCert(dnsNames []string) ([]byte, []byte, error) {
	if len(dnsNames) == 0 {
		return nil, nil, ErrWebhookCertDNSNamesRequired
	}

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey(dnsNames[0], nil, dnsNames)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate self-signed webhook certificate: %w", err)
	}

	return certPEM, keyPEM, nil
}

// certNeedsRenewal reports whether certPEM's NotAfter falls within
// renewalWindow of now.
func certNeedsRenewal(certPEM []byte, renewalWindow time.Duration, now time.Time) (bool, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, ErrWebhookCertPEMDecodeFailed
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse webhook certificate: %w", err)
	}

	return !cert.NotAfter.After(now.Add(renewalWindow)), nil
}
