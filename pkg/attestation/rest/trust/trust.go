// Package trust provides the handler for the trust endpoint.
package trust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

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
//
//nolint:funlen // Complexity is only apparent due to multiple error checks.
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

		if !report.CompliantWith(policy) {
			http.Error(response, "Machine is not trusted", http.StatusUnauthorized)

			return
		}

		response.WriteHeader(http.StatusNotImplemented)
	}
}

func getMachineAttestationReport(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	machine *clusterv1.Machine) (*restutils.Report, error) {
	configMapName := restutils.GetConfigMapReportName(machine)

	report, err := getReportConfigMap(ctx, kubeClient, configMapName)
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation report: %w", err)
	}

	return report, nil
}

func getAttestationPolicy(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	clusterName string) (*restutils.Report, error) {
	configMapName := fmt.Sprintf("%s-%s", configMapPolicyPrefix, clusterName)

	policy, err := getReportConfigMap(ctx, kubeClient, configMapName)
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation policy: %w", err)
	}

	return policy, nil
}

func getReportConfigMap(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	configMapName string) (*restutils.Report, error) {
	attestationConfigMap, err := restutils.GetConfigMapAPI(kubeClient).
		Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation policy config map: %w", err)
	}

	report := &restutils.Report{}

	err = json.Unmarshal([]byte(attestationConfigMap.Data["report"]), &report)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal attestation report: %w", err)
	}

	return report, nil
}
