//nolint:testpackage // white-box tests exercise unexported audit helpers
package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/audit/policy"
)

// TestLoadPolicyRuleEvaluatorDisabled asserts that audit is fully disabled when
// AuditDisabled is true, regardless of other settings.
func TestLoadPolicyRuleEvaluatorDisabled(t *testing.T) {
	t.Parallel()

	cfg := &config.KommodityConfig{
		AuditDisabled: true,
		// Even with a path set, disabled should win.
		AuditPolicyFilePath: "/nonexistent/path.yaml",
	}

	evaluator, err := loadPolicyRuleEvaluator(cfg)
	require.NoError(t, err)
	assert.Nil(t, evaluator, "expected nil evaluator when audit is disabled")
}

// TestLoadPolicyRuleEvaluatorCustomPath asserts that a custom policy file is
// loaded when AuditPolicyFilePath is set and audit is not disabled.
func TestLoadPolicyRuleEvaluatorCustomPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "audit-policy.yaml")

	policyContent := `apiVersion: audit.k8s.io/v1
kind: Policy
rules:
- level: Metadata
`
	err := os.WriteFile(policyPath, []byte(policyContent), 0o600)
	require.NoError(t, err)

	cfg := &config.KommodityConfig{
		AuditDisabled:       false,
		AuditPolicyFilePath: policyPath,
	}

	evaluator, err := loadPolicyRuleEvaluator(cfg)
	require.NoError(t, err)
	require.NotNil(t, evaluator, "expected non-nil evaluator for custom policy file")
}

// TestLoadPolicyRuleEvaluatorEmbedded asserts that the embedded default policy
// is loaded when no custom path is provided and audit is not disabled.
func TestLoadPolicyRuleEvaluatorEmbedded(t *testing.T) {
	t.Parallel()

	cfg := &config.KommodityConfig{
		AuditDisabled:       false,
		AuditPolicyFilePath: "",
	}

	evaluator, err := loadPolicyRuleEvaluator(cfg)
	require.NoError(t, err)
	require.NotNil(t, evaluator, "expected non-nil evaluator for embedded default policy")
}

// TestEmbeddedAuditPolicyIsValid asserts the embedded audit-policy.yaml parses
// successfully and contains at least one rule. This catches malformed YAML at
// test time rather than at startup.
func TestEmbeddedAuditPolicyIsValid(t *testing.T) {
	t.Parallel()

	auditPolicy, err := policy.LoadPolicyFromBytes(defaultAuditPolicy)
	require.NoError(t, err, "embedded audit policy should parse without error")
	require.NotNil(t, auditPolicy, "embedded audit policy should not be nil")
	assert.NotEmpty(t, auditPolicy.Rules, "embedded audit policy should contain at least one rule")
}
