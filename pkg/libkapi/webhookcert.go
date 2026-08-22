package libkapi

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// webhookCertFileName and webhookKeyFileName match webhook.Options'
	// own defaults (CertName, KeyName), so a certificate written here is
	// found by a webhook.Server built with default options.
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
// — reused as-is (not overridden) so a certificate written by
// writeWebhookCertFiles lands exactly where webhook.NewServer(webhook.Options{})
// already looks by default, and its certwatcher already watches for changes.
//
// os.TempDir() is the fixed, stable OS temp directory (e.g. "/tmp") — the
// same path every process on the host resolves to. This must NOT be
// os.MkdirTemp(), which mints a fresh, uniquely-named directory on every
// call: that would make every restart lose track of the certwatcher's
// target, breaking across-restart reuse and invalidating whatever
// caBundle the caller registered on a
// Validating/MutatingWebhookConfiguration.
//
//nolint:gochecknoglobals // fixed path, mirrors webhook.Options' own default CertDir.
var webhookCertDir = filepath.Join(os.TempDir(), "k8s-webhook-server", "serving-certs")

// writeWebhookCertFiles writes certPEM/keyPEM under webhookCertDir,
// creating the directory if needed - the same fixed path
// controller-runtime's webhook.Server certwatcher already watches (fsnotify
// plus a 10s poll fallback), so a rewrite here is picked up live, without a
// process restart. Shared by the initial sync (Server.syncWebhookCert) and
// the rotation loop (webhookcertrotation.go).
func writeWebhookCertFiles(certPEM []byte, keyPEM []byte) error {
	err := os.MkdirAll(webhookCertDir, webhookDirPerm)
	if err != nil {
		return fmt.Errorf("failed to create webhook certificate directory %q: %w", webhookCertDir, err)
	}

	certPath := filepath.Join(webhookCertDir, webhookCertFileName)

	err = os.WriteFile(certPath, certPEM, webhookCertPerm)
	if err != nil {
		return fmt.Errorf("failed to write webhook certificate %q: %w", certPath, err)
	}

	keyPath := filepath.Join(webhookCertDir, webhookKeyFileName)

	err = os.WriteFile(keyPath, keyPEM, webhookKeyPerm)
	if err != nil {
		return fmt.Errorf("failed to write webhook key %q: %w", keyPath, err)
	}

	return nil
}
