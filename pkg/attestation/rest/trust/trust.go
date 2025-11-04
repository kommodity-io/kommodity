// Package trust provides the handler for the trust endpoint.
package trust

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/net"
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

// GetTrust godoc
// @Summary  Check trust status for a machine
// @Tags     Attestation
// @Param    ip   path  string  true  "IPv4 or IPv6"
// @Success  200  {string}  string  "No content"
// @Failure  400  {object}  string  "If the request is invalid"
// @Failure  401  {string}  string  "No content"
// @Failure  404  {object}  string  "If the machine is not found"
// @Failure  405  {object}  string  "If the method is not allowed"
// @Failure  500  {object}  string  "If there is a server error"
// @Router   /report/{ip}/trust [get]
//
// GetTrust handles the trust status retrieval for a given attestation report.
func GetTrust(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		//nolint:varnamelen // Variable name ip is appropriate for the context.
		ip, err := net.GetOriginalIPFromRequest(request)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)

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

		machine, err := net.FindManagedMachineByIP(request.Context(), &ctrlClient, ip)
		if err != nil {
			if errors.Is(err, net.ErrNoMachineFound) {
				http.Error(response, "Machine not found", http.StatusNotFound)
			} else {
				http.Error(response, "Failed to find machine by IP", http.StatusInternalServerError)
			}

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
	configMapName := restutils.GetConfigMapReportName(machine)

	attestationConfigMap, err := restutils.GetConfigMapAPI(kubeClient).
		Get(ctx, configMapName, metav1.GetOptions{})
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

	attestationConfigMap, err := restutils.GetConfigMapAPI(kubeClient).
		Get(ctx, configMapName, metav1.GetOptions{})
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
