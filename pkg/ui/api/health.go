package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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

// GetClusterHealth returns an HTTP handler that checks cluster health via the /livez endpoint.
func GetClusterHealth(
	cfg *config.KommodityConfig,
	logger *zap.Logger,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx := request.Context()
		clusterName := request.PathValue("clusterName")

		if clusterName == "" {
			http.Error(writer, "Cluster name is required", http.StatusBadRequest)

			return
		}

		// Get cluster kubeconfig
		kubeconfigBytes, err := getClusterKubeconfigBytes(ctx, cfg, clusterName)
		if err != nil {
			logger.Warn("Failed to get cluster kubeconfig for health check",
				zap.String("cluster", clusterName),
				zap.Error(err),
			)
			writeHealthResponse(writer, false, "Unable to retrieve cluster configuration")

			return
		}

		// Check cluster health
		healthy, reason := checkClusterLivez(ctx, kubeconfigBytes, logger)
		writeHealthResponse(writer, healthy, reason)
	}
}

// getClusterKubeconfigBytes retrieves the raw kubeconfig bytes for a cluster.
func getClusterKubeconfigBytes(
	ctx context.Context,
	cfg *config.KommodityConfig,
	clusterName string,
) ([]byte, error) {
	// Create Kubernetes client
	kubeClient, err := kubernetes.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get kubeconfig secret
	secretName := clusterName + "-kubeconfig"
	secretAPI := kubeClient.CoreV1().Secrets(config.KommodityNamespace)

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

// checkClusterLivez checks the /livez endpoint of a cluster.
func checkClusterLivez(
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
	restConfig.Timeout = HealthCheckTimeoutSeconds * time.Second

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
	// #nosec G704 -- URL is derived from cluster kubeconfig, not user input
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, livezURL, nil)
	if err != nil {
		logger.Warn("Failed to create health check request", zap.Error(err))

		return false, "Health check error"
	}

	// Execute request
	// #nosec G704 -- URL is derived from cluster kubeconfig, not user input
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

	// Read response body for details
	body, _ := io.ReadAll(resp.Body)

	// Check status code
	if resp.StatusCode == http.StatusOK {
		return true, ""
	}

	reason := fmt.Sprintf("Health check returned status %d", resp.StatusCode)
	if len(body) > 0 && len(body) < 200 {
		reason = string(body)
	}

	return false, reason
}

// writeHealthResponse writes a ClusterHealthResponse to the HTTP response.
func writeHealthResponse(writer http.ResponseWriter, healthy bool, reason string) {
	response := ClusterHealthResponse{
		Healthy: healthy,
		Reason:  reason,
	}

	writer.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(writer).Encode(response)
	if err != nil {
		http.Error(writer, "Failed to encode response", http.StatusInternalServerError)
	}
}
