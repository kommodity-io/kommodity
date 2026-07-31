package server

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	auditv1 "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/audit/policy"
	pluginbuffered "k8s.io/apiserver/plugin/pkg/audit/buffered"
	pluginlog "k8s.io/apiserver/plugin/pkg/audit/log"
)

// defaultAuditPolicy is the embedded audit policy used when no custom policy
// file is provided via KOMMODITY_AUDIT_POLICY_FILE_PATH.
//
//go:embed audit-policy.yaml
var defaultAuditPolicy []byte

// zapWriter adapts a zap.Logger to io.Writer.
type zapWriter struct {
	logger *zap.Logger
}

func (w zapWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))

	w.logger.Info("audit", zap.String("raw", line))

	return len(p), nil
}

// loadPolicyRuleEvaluator builds the audit policy rule evaluator based on configuration.
// Precedence: disabled > custom file path > embedded default policy.
func loadPolicyRuleEvaluator(cfg *config.KommodityConfig) (audit.PolicyRuleEvaluator, error) {
	if cfg.AuditDisabled {
		// Audit disabled; return nil to skip audit entirely.
		//
		//nolint:nilnil // intentional: nil evaluator signals no audit
		return nil, nil
	}

	policyFilePath := cfg.AuditPolicyFilePath
	if policyFilePath != "" {
		auditPolicy, err := policy.LoadPolicyFromFile(policyFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load audit policy from file: %w", err)
		}

		return policy.NewPolicyRuleEvaluator(auditPolicy), nil
	}

	auditPolicy, err := policy.LoadPolicyFromBytes(defaultAuditPolicy)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded audit policy: %w", err)
	}

	return policy.NewPolicyRuleEvaluator(auditPolicy), nil
}

func getPolicyBackend(ctx context.Context) audit.Backend {
	// Create a dedicated audit logger at Info level, independent of the global
	// LOG_LEVEL setting. Audit events must always be captured for compliance.
	auditLogger := newAuditLogger(ctx)

	logBackend := pluginlog.NewBackend(
		zapWriter{logger: auditLogger},
		pluginlog.FormatJson,
		auditv1.SchemeGroupVersion)

	//nolint:mnd // Default configuration from upstream Kubernetes
	return pluginbuffered.NewBackend(logBackend, pluginbuffered.BatchConfig{
		ThrottleEnable: false,
		MaxBatchSize:   100,
		MaxBatchWait:   10 * time.Second,
		BufferSize:     1000,
	})
}

// newAuditLogger creates a production zap.Logger pinned to Info level so audit
// events are never silenced by a higher global LOG_LEVEL. Output goes to stdout
// and sampling is disabled to ensure no audit events are dropped under load.
func newAuditLogger(ctx context.Context) *zap.Logger {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	config.OutputPaths = []string{"stdout"}
	config.Sampling = nil

	logger, err := config.Build()
	if err != nil {
		// Fall back to the standard logger from context if construction fails.
		return logging.FromContext(ctx)
	}

	return logger
}
