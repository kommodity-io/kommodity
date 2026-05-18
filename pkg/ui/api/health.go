package api

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/config"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClusterHealthResponse holds the health status of a cluster.
type ClusterHealthResponse struct {
	Healthy bool   `json:"healthy"`
	Reason  string `json:"reason,omitempty"`
}

// GetClusterKubeconfigBytes retrieves the raw kubeconfig bytes for a cluster.
func GetClusterKubeconfigBytes(
	ctx context.Context,
	cfg *config.KommodityConfig,
	clusterName string,
) ([]byte, error) {
	// Create Kubernetes client
	kubeClient, err := kubernetes.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get kubeconfig secret (CAPI creates it in the cluster's namespace).
	secretName := clusterName + "-kubeconfig"
	secretAPI := kubeClient.CoreV1().Secrets(clusterName)

	secret, err := secretAPI.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret: %w", err)
	}

	kubeconfigBytes, ok := secret.Data["value"]
	if !ok || kubeconfigBytes == nil {
		return nil, fmt.Errorf("%w: %s", ErrKubeConfigSecretIsEmpty, secretName)
	}

	return kubeconfigBytes, nil
}

// CheckClusterLivez checks the /livez endpoint of a cluster.
func CheckClusterLivez(
	ctx context.Context,
	kubeconfigBytes []byte,
	logger *zap.Logger,
) (bool, string) {
	// Parse kubeconfig and get REST config
	restConfig, err := getRESTConfig(kubeconfigBytes, logger)
	if err != nil {
		return false, "Invalid cluster configuration"
	}

	// Create HTTP client with timeout
	restConfig.Timeout = HealthCheckTimeout

	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		logger.Warn("Failed to create HTTP client", zap.Error(err))

		return false, "Unable to connect to cluster"
	}

	// Check livez endpoint
	return executeLivezCheck(ctx, httpClient, restConfig.Host, logger)
}

// getRESTConfig parses kubeconfig bytes and returns a REST config.
func getRESTConfig(kubeconfigBytes []byte, logger *zap.Logger) (*rest.Config, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfigBytes)
	if err != nil {
		logger.Warn("Failed to parse kubeconfig", zap.Error(err))

		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		logger.Warn("Failed to get REST config", zap.Error(err))

		return nil, fmt.Errorf("failed to get REST config: %w", err)
	}

	return restConfig, nil
}

// executeLivezCheck performs the HTTP request to check cluster health.
func executeLivezCheck(
	ctx context.Context,
	httpClient *http.Client,
	host string,
	logger *zap.Logger,
) (bool, string) {
	livezURL := host + "/livez"

	// Create request with context
	// #nosec G107 -- URL is derived from cluster kubeconfig, not user input
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, livezURL, nil)
	if err != nil {
		logger.Warn("Failed to create health check request", zap.Error(err))

		return false, "Health check error"
	}

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Debug("Cluster health check failed",
			zap.String("url", livezURL),
			zap.Error(err),
		)

		return false, "Cluster unreachable"
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Warn("Failed to close response body", zap.Error(closeErr))
		}
	}()

	// Check status code
	if resp.StatusCode == http.StatusOK {
		return true, ""
	}

	// Read response body for details (limited to prevent memory exhaustion)
	limitedReader := io.LimitReader(resp.Body, MaxHealthResponseBytes)

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		logger.Warn("Failed to read response body", zap.Error(err))

		return false, fmt.Sprintf("Health check returned status %d", resp.StatusCode)
	}

	reason := fmt.Sprintf("Health check returned status %d", resp.StatusCode)
	if len(body) > 0 {
		reason = string(body)
	}

	return false, reason
}
