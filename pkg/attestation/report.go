package attestation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	configMapReportPrefix = "attestation-report"
)

type reportRequest struct {
	Nounce   string          `json:"nounce"`
	NodeUUID string          `json:"nodeUUID"` //nolint:tagliatelle
	NodeIP   string          `json:"nodeIP"`   //nolint:tagliatelle
	Report   json.RawMessage `json:"report"`
}

func postReport(nounceStore *NounceStore, cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
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

		valid, err := nounceStore.use(req.Nounce)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)

			return
		}

		if !valid {
			http.Error(response, "Invalid nounce", http.StatusUnauthorized)

			return
		}

		kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
		if err != nil {
			http.Error(response, "Failed to create kube client", http.StatusInternalServerError)

			return
		}

		err = saveAttestationReport(request.Context(), kubeClient, req.NodeUUID, req.NodeIP, req.Report)
		if err != nil {
			http.Error(response, "Failed to save attestation report", http.StatusInternalServerError)

			return
		}

		response.WriteHeader(http.StatusOK)
	}
}

func saveAttestationReport(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	nodeUUID, nodeIP string,
	report json.RawMessage,
) error {
	_, err := getConfigMapAPI(kubeClient).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getConfigMapName(nodeUUID),
			Namespace: config.KommodityNamespace,
			Labels:    config.GetKommodityLabels(nodeUUID, nodeIP),
		},
		Data: map[string]string{
			"report": string(report),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to save attestation report for node %s: %w", nodeUUID, err)
	}

	return nil
}

func getConfigMapName(nodeUUID string) string {
	return fmt.Sprintf("%s-%s", configMapReportPrefix, nodeUUID)
}

func getConfigMapAPI(kubeClient *clientgoclientset.Clientset) v1.ConfigMapInterface {
	return kubeClient.CoreV1().ConfigMaps(config.KommodityNamespace)
}
