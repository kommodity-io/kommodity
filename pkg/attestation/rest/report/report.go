// Package report provides the handler for the report endpoint.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/net"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

// AttestationReportRequest represents the request structure for the attestation report endpoint.
type AttestationReportRequest struct {
	Nonce  string           `example:"884f2638c74645b859f87e76560748cc" json:"nonce"`
	Node   NodeInfo         `json:"node"`
	Report restutils.Report `json:"report"`
}

// NodeInfo represents information about the node submitting the attestation report.
type NodeInfo struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
}

// PostReport godoc
// @Summary  Submit attestation report
// @Tags     Attestation
// @Accept   json
// @Produce  json
// @Param    payload  body  AttestationReportRequest  true  "Report"
// @Success  200  {string}  string   "No content"
// @Failure  400  {object}  string   "If the request is invalid"
// @Failure  401  {object}  string   "If the nonce is invalid"
// @Failure  405  {object}  string   "If the method is not allowed"
// @Failure  500  {object}  string   "If there is a server error"
// @Router   /report [post]
//
// PostReport handles the POST /report endpoint.
func PostReport(nonceStore *restutils.NonceStore,
	cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		var req AttestationReportRequest

		err := json.NewDecoder(request.Body).Decode(&req)
		if err != nil {
			http.Error(response, "Failed to decode request", http.StatusBadRequest)

			return
		}

		valid, err := nonceStore.Use(request.RemoteAddr, req.Nonce)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)

			return
		}

		if !valid {
			http.Error(response, "Invalid nonce", http.StatusUnauthorized)

			return
		}

		err = saveAttestationReport(request.Context(), cfg, req)
		if err != nil {
			http.Error(response, "Failed to save attestation report", http.StatusInternalServerError)

			return
		}

		response.WriteHeader(http.StatusOK)
	}
}

func saveAttestationReport(ctx context.Context, cfg *config.KommodityConfig, request AttestationReportRequest) error {
	kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	ctrlClient, err := ctrlclint.New(cfg.ClientConfig.LoopbackClientConfig, ctrlclint.Options{})
	if err != nil {
		return fmt.Errorf("failed to create controller client: %w", err)
	}

	machine, err := net.FindManagedMachineByIP(ctx, &ctrlClient, request.Node.IP)
	if err != nil {
		return fmt.Errorf("failed to find managed machine by IP %s: %w", request.Node.IP, err)
	}

	jsonReport, err := json.Marshal(request.Report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	labels := config.GetKommodityLabels(request.Node.UUID, request.Node.IP)
	resourceName := restutils.GetConfigMapReportName(machine)

	_, err = restutils.GetConfigMapAPI(kubeClient).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: config.KommodityNamespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"report": string(jsonReport),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to save attestation report for node %s: %w", request.Node.IP, err)
	}

	_, err = restutils.GetSecretAPI(kubeClient).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: config.KommodityNamespace,
			Labels:    labels,
		},
		StringData: map[string]string{
			"nonce": request.Nonce,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to save node secret for node %s: %w", request.Node.IP, err)
	}

	return nil
}
