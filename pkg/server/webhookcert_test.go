//nolint:testpackage // white-box tests exercise unexported webhook cert helpers
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestGenerateSelfSignedWebhookCertVerifiesAgainstItself asserts the generated certificate is a
// valid serving key pair AND verifies against itself as a CA. This is the property the conversion
// webhook relies on: the same PEM is served on localhost and used as the CRD caBundle.
func TestGenerateSelfSignedWebhookCertVerifiesAgainstItself(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM, err := generateSelfSignedWebhookCert()
	if err != nil {
		t.Fatalf("generateSelfSignedWebhookCert() returned error: %v", err)
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("generated cert/key is not a valid TLS key pair: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add generated cert to CA pool")
	}

	leaf := parseFirstCert(t, certPEM)

	if !leaf.IsCA {
		t.Error("expected generated certificate to be a CA (so it can serve as its own caBundle)")
	}

	// Verifying the leaf against a pool containing only itself proves caBundle == served cert works.
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool})
	if err != nil {
		t.Fatalf("certificate does not verify against itself as caBundle: %v", err)
	}

	err = leaf.VerifyHostname("localhost")
	if err != nil {
		t.Errorf("certificate is not valid for localhost: %v", err)
	}
}

// TestGetOrCreateWebhookServingCertIsStable asserts the certificate is persisted and identical
// across calls — i.e. it survives "restarts" (repeated calls against the same backing store), which
// is what keeps the persisted CRD caBundle valid.
func TestGetOrCreateWebhookServingCertIsStable(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	ctx := context.Background()

	cert1, key1, err := getOrCreateWebhookServingCert(ctx, client.CoreV1())
	if err != nil {
		t.Fatalf("first getOrCreateWebhookServingCert() returned error: %v", err)
	}

	cert2, key2, err := getOrCreateWebhookServingCert(ctx, client.CoreV1())
	if err != nil {
		t.Fatalf("second getOrCreateWebhookServingCert() returned error: %v", err)
	}

	if !bytes.Equal(cert1, cert2) {
		t.Error("certificate changed between calls; it must be stable across restarts")
	}

	if !bytes.Equal(key1, key2) {
		t.Error("private key changed between calls; it must be stable across restarts")
	}

	secret, err := client.CoreV1().Secrets("kommodity-system").
		Get(ctx, webhookServingCertSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected webhook serving cert secret to be persisted: %v", err)
	}

	if !bytes.Equal(secret.Data[corev1.TLSCertKey], cert1) {
		t.Error("persisted secret cert does not match returned cert")
	}
}

func TestWebhookCertDataFromSecretMissingData(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		Data: map[string][]byte{
			corev1.TLSCertKey: []byte("cert-only"),
		},
	}

	_, _, err := webhookCertDataFromSecret(secret)
	if err == nil {
		t.Fatal("expected error when private key is missing from secret, got nil")
	}
}

func parseFirstCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	return cert
}
