package libkapi //nolint:testpackage // exercises unexported internals: webhookCertDir, writeWebhookCertFiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	certutil "k8s.io/client-go/util/cert"
)

// TestWriteWebhookCertFiles_WritesFiles verifies that writeWebhookCertFiles
// writes tls.crt/tls.key at the expected, fixed file names.
//
//nolint:paralleltest // mutates the package-global webhookCertDir
func TestWriteWebhookCertFiles_WritesFiles(t *testing.T) {
	withTempWebhookCertDir(t)

	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	err = writeWebhookCertFiles(certPEM, keyPEM)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(webhookCertDir, webhookCertFileName))
	assert.FileExists(t, filepath.Join(webhookCertDir, webhookKeyFileName))

	writtenCert, err := os.ReadFile(filepath.Join(webhookCertDir, webhookCertFileName))
	require.NoError(t, err)
	assert.Equal(t, certPEM, writtenCert)

	writtenKey, err := os.ReadFile(filepath.Join(webhookCertDir, webhookKeyFileName))
	require.NoError(t, err)
	assert.Equal(t, keyPEM, writtenKey)
}

// TestWriteWebhookCertFiles_OverwritesExisting is a regression test for the
// rotation requirement: unlike the old ensure-if-absent generation,
// writeWebhookCertFiles must unconditionally overwrite whatever is already
// on disk, so a rotated certificate actually replaces the previous one for
// controller-runtime's certwatcher to pick up.
//
//nolint:paralleltest // mutates the package-global webhookCertDir
func TestWriteWebhookCertFiles_OverwritesExisting(t *testing.T) {
	withTempWebhookCertDir(t)

	firstCertPEM, firstKeyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	err = writeWebhookCertFiles(firstCertPEM, firstKeyPEM)
	require.NoError(t, err)

	secondCertPEM, secondKeyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", nil, []string{"localhost"})
	require.NoError(t, err)

	err = writeWebhookCertFiles(secondCertPEM, secondKeyPEM)
	require.NoError(t, err)

	writtenCert, err := os.ReadFile(filepath.Join(webhookCertDir, webhookCertFileName))
	require.NoError(t, err)
	assert.Equal(t, secondCertPEM, writtenCert)
	assert.NotEqual(t, firstCertPEM, secondCertPEM, "test fixture sanity check: two generated certs should differ")
}

// withTempWebhookCertDir points the package-level webhookCertDir at a fresh
// t.TempDir() for the duration of the test, restoring it on cleanup. Not
// safe to use from a t.Parallel() test - webhookCertDir is shared
// process-wide state (see its own doc comment).
func withTempWebhookCertDir(t *testing.T) {
	t.Helper()

	original := webhookCertDir
	webhookCertDir = t.TempDir()

	t.Cleanup(func() { webhookCertDir = original })
}
