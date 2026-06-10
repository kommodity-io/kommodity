package server

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	// webhookServingCertSecretName is the Secret that persists the conversion/admission
	// webhook serving certificate. The certificate is generated once and reused on every
	// restart so the caBundle injected into the CRDs (which is persisted in the backing
	// store) stays valid across process restarts. See getOrCreateWebhookServingCert.
	webhookServingCertSecretName = "webhook-serving-cert"
	// webhookCertSerialBits is the bit size of the random certificate serial number.
	webhookCertSerialBits = 128
	// webhookCertValidity is the lifetime of the self-signed webhook serving certificate.
	// It is long-lived because it is persisted and reused; the endpoint is loopback-only
	// (https://localhost:<WebhookPort>), reachable only by the in-process apiserver.
	webhookCertValidity = 100 * 365 * 24 * time.Hour
	// webhookCertBackdate offsets NotBefore into the past to tolerate clock skew.
	webhookCertBackdate = time.Hour
)

// getOrCreateWebhookServingCert returns the PEM-encoded serving certificate and key for the
// in-process webhook server, persisting them in a Kubernetes Secret so they survive restarts.
//
// The certificate is self-signed and marked as a CA, so the same PEM serves as both the TLS
// serving certificate and the caBundle that the apiserver uses to verify the webhook endpoint.
// Returning a stable certificate is what keeps the conversion-webhook caBundle (persisted in the
// CRDs) consistent with the cert actually served on localhost across every process restart.
//
// It is idempotent and safe to call concurrently from multiple PostStartHooks: the first caller
// creates the Secret, and concurrent callers fall back to reading the created Secret.
//
// Returns the serving certificate PEM and the private key PEM, in that order.
func getOrCreateWebhookServingCert(
	ctx context.Context,
	client corev1client.CoreV1Interface,
) ([]byte, []byte, error) {
	secret, getErr := client.Secrets(config.KommodityNamespace).
		Get(ctx, webhookServingCertSecretName, metav1.GetOptions{})
	if getErr == nil {
		cert, key, dataErr := webhookCertDataFromSecret(secret)
		if dataErr == nil {
			return cert, key, nil
		}
		// The Secret exists but is malformed; fall through to regenerate and overwrite it.
	} else if !apierrors.IsNotFound(getErr) {
		return nil, nil, fmt.Errorf("failed to get webhook serving cert secret: %w", getErr)
	}

	certPEM, keyPEM, err := generateSelfSignedWebhookCert()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate webhook serving cert: %w", err)
	}

	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookServingCertSecretName,
			Namespace: config.KommodityNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
	}

	_, createErr := client.Secrets(config.KommodityNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if createErr == nil {
		return certPEM, keyPEM, nil
	}

	if !apierrors.IsAlreadyExists(createErr) {
		return nil, nil, fmt.Errorf("failed to create webhook serving cert secret: %w", createErr)
	}

	// A concurrent caller created the Secret first; re-read and use its certificate so all
	// consumers agree on a single serving certificate.
	existing, reGetErr := client.Secrets(config.KommodityNamespace).
		Get(ctx, webhookServingCertSecretName, metav1.GetOptions{})
	if reGetErr != nil {
		return nil, nil, fmt.Errorf("failed to get existing webhook serving cert secret: %w", reGetErr)
	}

	cert, key, dataErr := webhookCertDataFromSecret(existing)
	if dataErr != nil {
		return nil, nil, dataErr
	}

	return cert, key, nil
}

// webhookCertDataFromSecret extracts and validates the cert/key PEM from the Secret.
// Returns the serving certificate PEM and the private key PEM, in that order.
func webhookCertDataFromSecret(secret *corev1.Secret) ([]byte, []byte, error) {
	cert, hasCert := secret.Data[corev1.TLSCertKey]
	key, hasKey := secret.Data[corev1.TLSPrivateKeyKey]

	if !hasCert || !hasKey || len(cert) == 0 || len(key) == 0 {
		return nil, nil, fmt.Errorf("%w: webhook serving cert secret missing %s/%s",
			ErrDataMissingFromSecret, corev1.TLSCertKey, corev1.TLSPrivateKeyKey)
	}

	return cert, key, nil
}

// generateSelfSignedWebhookCert generates a long-lived self-signed CA certificate usable both as
// the loopback webhook serving certificate and as its own caBundle.
// Returns the certificate PEM and the private key PEM, in that order.
func generateSelfSignedWebhookCert() ([]byte, []byte, error) {
	key, err := generateRSAPrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate webhook private key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), webhookCertSerialBits)

	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate certificate serial number: %w", err)
	}

	now := time.Now()

	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost", "apiserver-loopback-client"},
		IPAddresses:           []net.IP{net.ParseIP(loopbackBindAddress)},
		NotBefore:             now.Add(-webhookCertBackdate),
		NotAfter:              now.Add(webhookCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create webhook certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := convertRSAKeyToPEM(key)

	return certPEM, keyPEM, nil
}

// ensureKommodityNamespace creates the Kommodity system namespace if it does not yet exist.
// It tolerates a concurrent creation race with the bootstrap-required-resources hook.
func ensureKommodityNamespace(ctx context.Context, client corev1client.CoreV1Interface) error {
	_, err := client.Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: config.KommodityNamespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to ensure namespace %q exists: %w", config.KommodityNamespace, err)
	}

	return nil
}
