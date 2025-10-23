package attestation

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
)

const (
	configMapPolicyPrefix = "attestation-policy"
)

type attestationReport struct {
}

type attestationPolicy struct {
	ConfidentialCompute bool `json:"confidentialCompute"`
	SecureBoot          bool `json:"secureBoot"`
	ImageSignature      bool `json:"imageSignature"`
}

// getTrust handles the trust status retrieval for a given attestation report.
// 200 OK if trusted, 401 Unauthorized if not trusted.
func getTrust(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		ip := request.PathValue("ip")
		if ip == "" {
			http.Error(response, "IP address is required", http.StatusBadRequest)

			return
		}

		kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
		if err != nil {
			http.Error(response, "Failed to create kube client", http.StatusInternalServerError)

			return
		}

		_ = kubeClient

		// Call K8s API to get all Machines and validate that we have a
		// node with specific IP and is in boostrapping state.

		response.WriteHeader(http.StatusNotImplemented)
	}
}

func getAttestationPolicy(ctx context.Context, kubeClient *clientgoclientset.Clientset, releaseName string) (*attestationPolicy, error) {
	cmName := fmt.Sprintf("%s-%s", configMapPolicyPrefix, releaseName)

	attestationConfigMap, err := getConfigMapAPI(kubeClient).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation policy config map: %w", err)
	}

	policy := &attestationPolicy{}

	confidentialCompute, err := strconv.ParseBool(attestationConfigMap.Data["confidentialCompute"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse confidentialCompute: %w", err)
	}

	secureBoot, err := strconv.ParseBool(attestationConfigMap.Data["secureBoot"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse secureBoot: %w", err)
	}

	imageSignature, err := strconv.ParseBool(attestationConfigMap.Data["imageSignature"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse imageSignature: %w", err)
	}

	policy.ConfidentialCompute = confidentialCompute
	policy.SecureBoot = secureBoot
	policy.ImageSignature = imageSignature

	return policy, nil
}
