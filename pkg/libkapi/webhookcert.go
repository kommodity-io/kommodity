package libkapi

import (
	"fmt"
	"os"
	"path/filepath"

	certutil "k8s.io/client-go/util/cert"
)

const (
	// webhookCertFileName and webhookKeyFileName match webhook.Options'
	// own defaults (CertName, KeyName), so a self-signed cert generated here
	// is found by a webhook.Server built with default options.
	webhookCertFileName = "tls.crt"
	webhookKeyFileName  = "tls.key"

	// webhookCertPerm/webhookKeyPerm: the key is private (owner read/write
	// only); the cert is the public half, safe to be world-readable like any
	// other cert file.
	webhookCertPerm = 0o644
	webhookKeyPerm  = 0o600
	webhookDirPerm  = 0o700
)

// webhookCertDir is controller-runtime's own default webhook.Options.CertDir
// — reused as-is (not overridden) so the certificate ensureSelfSignedWebhookCert
// generates lands exactly where webhook.NewServer(webhook.Options{}) already
// looks by default.
//
// os.TempDir() is the fixed, stable OS temp directory (e.g. "/tmp") — the
// same path every process on the host resolves to. This must NOT be
// os.MkdirTemp(), which mints a fresh, uniquely-named directory on every
// call: that would make every restart generate (and immediately orphan) a
// new certificate, breaking across-restart reuse and invalidating whatever
// caBundle the caller registered on a
// Validating/MutatingWebhookConfiguration.
//
//nolint:gochecknoglobals // fixed path, mirrors webhook.Options' own default CertDir.
var webhookCertDir = filepath.Join(os.TempDir(), "k8s-webhook-server", "serving-certs")

// ensureSelfSignedWebhookCert writes tls.crt/tls.key under webhookCertDir if
// they don't already exist there, using k8s.io/client-go/util/cert's
// GenerateSelfSignedCertKey — the same helper k8s.io/apiserver's own
// loopback-client cert generation uses (see apiserver.go's
// newLoopbackClientConfig doc) — so repeated New calls against the same
// /tmp reuse one certificate instead of generating (and orphaning) a new
// one every time.
func ensureSelfSignedWebhookCert(dnsNames []string) error {
	certPath := filepath.Join(webhookCertDir, webhookCertFileName)
	keyPath := filepath.Join(webhookCertDir, webhookKeyFileName)

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	if certErr == nil && keyErr == nil {
		return nil
	}

	err := os.MkdirAll(webhookCertDir, webhookDirPerm)
	if err != nil {
		return fmt.Errorf("failed to create webhook certificate directory %q: %w", webhookCertDir, err)
	}

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey(dnsNames[0], nil, dnsNames)
	if err != nil {
		return fmt.Errorf("failed to generate self-signed webhook certificate: %w", err)
	}

	err = os.WriteFile(certPath, certPEM, webhookCertPerm)
	if err != nil {
		return fmt.Errorf("failed to write webhook certificate %q: %w", certPath, err)
	}

	err = os.WriteFile(keyPath, keyPEM, webhookKeyPerm)
	if err != nil {
		return fmt.Errorf("failed to write webhook key %q: %w", keyPath, err)
	}

	return nil
}
