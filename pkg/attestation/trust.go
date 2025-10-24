package attestation

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
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

		//nolint:varnamelen // Variable name ip is appropriate for the context.
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

		ctrlClient, err := ctrlclint.New(cfg.ClientConfig.LoopbackClientConfig, ctrlclint.Options{})
		if err != nil {
			http.Error(response, "Failed to create controller client", http.StatusInternalServerError)

			return
		}

		machine, err := findManagedMachineByIP(request.Context(), &ctrlClient, ip)
		if err != nil {
			http.Error(response, "Failed to find machine by IP: "+err.Error(), http.StatusInternalServerError)

			return
		}

		policy, err := getAttestationPolicy(request.Context(), kubeClient, machine.Spec.ClusterName)
		if err != nil {
			http.Error(response, "Failed to get attestation policy: "+err.Error(), http.StatusInternalServerError)

			return
		}

		report, err := getMachineAttestationReport(request.Context(), kubeClient, machine)
		if err != nil {
			http.Error(response, "Failed to get attestation report: "+err.Error(), http.StatusInternalServerError)

			return
		}

		_ = report
		_ = policy

		// Compare report with policy to determine trust status.
		// For now, we return Not Implemented.

		response.WriteHeader(http.StatusNotImplemented)
	}
}

func getMachineAttestationReport(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	machine *clusterv1.Machine) (*attestationReport, error) {
	configMapName := getConfigMapReportName(machine)

	attestationConfigMap, err := getConfigMapAPI(kubeClient).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation report config map: %w", err)
	}

	// Define attestationReport structure and populate it from attestationConfigMap.Data
	_ = attestationConfigMap

	return &attestationReport{}, nil
}

func getAttestationPolicy(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	clusterName string) (*attestationPolicy, error) {
	configMapName := fmt.Sprintf("%s-%s", configMapPolicyPrefix, clusterName)

	attestationConfigMap, err := getConfigMapAPI(kubeClient).Get(ctx, configMapName, metav1.GetOptions{})
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
