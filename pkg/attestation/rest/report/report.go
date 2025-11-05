// Package report provides the handler for the report endpoint.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	Nounce string   `example:"884f2638c74645b859f87e76560748cc" json:"nounce"`
	Node   NodeInfo `json:"node"`
	Report Report   `json:"report"`
}

// NodeInfo represents information about the node submitting the attestation report.
type NodeInfo struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
}

// Report represents the attestation report structure.
type Report struct {
	Components []ComponentReport `json:"components"`
	Timestamp  time.Time         `json:"timestamp"`
}

// ComponentReport represents the attestation report for a specific component.
type ComponentReport struct {
	Name        string            `json:"name"`
	PCRs        map[int]string    `json:"pcrs"`
	Measurement string            `json:"measurement"` // SHA256 of the component
	Quote       string            `json:"quote"`       // SHA256 TPM quote (includes nonce)
	Signature   string            `json:"signature"`   // SHA256 TPM signature over quote
	Evidence    map[string]string `json:"evidence"`
}

// PostReport godoc
// @Summary  Submit attestation report
// @Tags     Attestation
// @Accept   json
// @Produce  json
// @Param    payload  body  AttestationReportRequest  true  "Report"
// @Success  200  {string}  string   "No content"
// @Failure  400  {object}  string   "If the request is invalid"
// @Failure  401  {object}  string   "If the nounce is invalid"
// @Failure  405  {object}  string   "If the method is not allowed"
// @Failure  500  {object}  string   "If there is a server error"
// @Router   /report [post]
//
// PostReport handles the POST /report endpoint.
func PostReport(nounceStore *restutils.NounceStore,
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

		valid, err := nounceStore.Use(request.RemoteAddr, req.Nounce)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)

			return
		}

		if !valid {
			http.Error(response, "Invalid nounce", http.StatusUnauthorized)

			return
		}

		err = saveAttestationReport(request.Context(), cfg, req.Node, req.Report)
		if err != nil {
			http.Error(response, "Failed to save attestation report", http.StatusInternalServerError)

			return
		}

		response.WriteHeader(http.StatusOK)
	}
}

func saveAttestationReport(ctx context.Context,
	cfg *config.KommodityConfig,
	node NodeInfo,
	report Report,
) error {
	kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	ctrlClient, err := ctrlclint.New(cfg.ClientConfig.LoopbackClientConfig, ctrlclint.Options{})
	if err != nil {
		return fmt.Errorf("failed to create controller client: %w", err)
	}

	machine, err := net.FindManagedMachineByIP(ctx, &ctrlClient, node.IP)
	if err != nil {
		return fmt.Errorf("failed to find managed machine by IP %s: %w", node.IP, err)
	}

	jsonReport, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	_, err = restutils.GetConfigMapAPI(kubeClient).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restutils.GetConfigMapReportName(machine),
			Namespace: config.KommodityNamespace,
			Labels:    config.GetKommodityLabels(node.UUID, node.IP),
		},
		Data: map[string]string{
			"report": string(jsonReport),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to save attestation report for node %s: %w", node.IP, err)
	}

	return nil
}
