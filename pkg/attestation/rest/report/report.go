// Package report provides the handler for the report endpoint.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	restutils "github.com/kommodity-io/kommodity/pkg/attestation/rest"
	"github.com/kommodity-io/kommodity/pkg/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

type reportRequest struct {
	Nounce string          `json:"nounce"`
	Node   nodeInfo        `json:"node"`
	Report json.RawMessage `json:"report"`
}

type nodeInfo struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
}

// PostReport handles the POST /report endpoint.
func PostReport(nounceStore *restutils.NounceStore,
	cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		var req reportRequest

		err := json.NewDecoder(request.Body).Decode(&req)
		if err != nil {
			http.Error(response, "Failed to decode request", http.StatusBadRequest)

			return
		}

		valid, err := nounceStore.Use(req.Nounce)
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
	node nodeInfo,
	report json.RawMessage,
) error {
	kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	ctrlClient, err := ctrlclint.New(cfg.ClientConfig.LoopbackClientConfig, ctrlclint.Options{})
	if err != nil {
		return fmt.Errorf("failed to create controller client: %w", err)
	}

	machine, err := restutils.FindManagedMachineByIP(ctx, &ctrlClient, node.IP)
	if err != nil {
		return fmt.Errorf("failed to find managed machine by IP %s: %w", node.IP, err)
	}

	_, err = restutils.GetConfigMapAPI(kubeClient).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restutils.GetConfigMapReportName(machine),
			Namespace: config.KommodityNamespace,
			Labels:    config.GetKommodityLabels(node.UUID, node.IP),
		},
		Data: map[string]string{
			"report": string(report),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to save attestation report for node %s: %w", node.IP, err)
	}

	return nil
}
