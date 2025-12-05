package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	auditv1 "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/audit/policy"
	pluginbuffered "k8s.io/apiserver/plugin/pkg/audit/buffered"
	pluginlog "k8s.io/apiserver/plugin/pkg/audit/log"
)

// zapWriter adapts a zap.Logger to io.Writer.
type zapWriter struct {
	logger *zap.Logger
}

func (w zapWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))

	w.logger.Info("audit", zap.String("raw", line))

	return len(p), nil
}

func loadPolicyRuleEvaluator(cfg *config.KommodityConfig) (audit.PolicyRuleEvaluator, error) {
	policyFilePath := cfg.AuditPolicyFilePath
	if policyFilePath == "" {
		// No audit policy file path provided; return nil to use default behavior
		//
		//nolint:nilnil
		return nil, nil
	}

	auditPolicy, err := policy.LoadPolicyFromFile(policyFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load audit policy from file: %w", err)
	}

	return policy.NewPolicyRuleEvaluator(auditPolicy), nil
}

func getPolicyBackend(ctx context.Context) audit.Backend {
	logger := logging.FromContext(ctx)

	logBackend := pluginlog.NewBackend(
		zapWriter{logger: logger},
		pluginlog.FormatJson,
		auditv1.SchemeGroupVersion)

	// Default configuration from upstream Kubernetes
	return pluginbuffered.NewBackend(logBackend, pluginbuffered.BatchConfig{
		ThrottleEnable: false,
		MaxBatchSize:   100,
		MaxBatchWait:   10 * time.Second,
		BufferSize:     1000,
	})
}
