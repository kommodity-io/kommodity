package talosproxy

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

const (
	envHTTPSProxy = "HTTPS_PROXY"
	envNoProxy    = "NO_PROXY"

	// noProxyDefaults are the addresses that must bypass the proxy to prevent loops.
	noProxyDefaults = "localhost,127.0.0.0/8"
)

// SetProxyEnv configures the HTTPS_PROXY and NO_PROXY environment variables
// so that the Talos client's DynamicProxyDialer routes connections through the
// local HTTP CONNECT proxy.
//
// If HTTPS_PROXY is already set (e.g., corporate proxy), a warning is logged
// and the existing value is NOT overridden.
func SetProxyEnv(logger *zap.Logger, listenAddr string) error {
	existing := os.Getenv(envHTTPSProxy)
	if existing != "" {
		logger.Warn("HTTPS_PROXY is already set, not overriding — Talos proxy routing may not work",
			zap.String("existingValue", existing),
			zap.String("desiredValue", listenAddr))

		return nil
	}

	proxyURL := "http://" + listenAddr

	err := os.Setenv(envHTTPSProxy, proxyURL)
	if err != nil {
		return fmt.Errorf("failed to set %s: %w", envHTTPSProxy, err)
	}

	err = appendNoProxy()
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", envNoProxy, err)
	}

	logger.Info("Configured proxy environment variables",
		zap.String(envHTTPSProxy, proxyURL),
		zap.String(envNoProxy, os.Getenv(envNoProxy)))

	return nil
}

// appendNoProxy appends the default no-proxy addresses to the NO_PROXY
// environment variable, avoiding duplicates.
func appendNoProxy() error {
	current := os.Getenv(envNoProxy)

	parts := make([]string, 0)
	if current != "" {
		parts = append(parts, current)
	}

	for _, entry := range strings.Split(noProxyDefaults, ",") {
		if !strings.Contains(current, entry) {
			parts = append(parts, entry)
		}
	}

	err := os.Setenv(envNoProxy, strings.Join(parts, ","))
	if err != nil {
		return fmt.Errorf("failed to set %s: %w", envNoProxy, err)
	}

	return nil
}
