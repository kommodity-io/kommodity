// Package userdata provides handlers for user data metadata.
package userdata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/attestation"
	"github.com/kommodity-io/kommodity/pkg/config"
	restutils "github.com/kommodity-io/kommodity/pkg/metadata/rest"
	"github.com/kommodity-io/kommodity/pkg/net"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"go.yaml.in/yaml/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetUserData handles requests for user data metadata.
//
//nolint:funlen,cyclop // Complexity is only apparent due to multiple error checks.
func GetUserData(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
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

		trusted, err := isTrusted(request.Context(), ip, cfg)
		if err != nil {
			http.Error(response, "Failed to verify trust status", http.StatusInternalServerError)

			return
		}

		if !trusted {
			http.Error(response, "Unauthorized: machine is not trusted", http.StatusUnauthorized)

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

		machineConfig, err := fetchMachineConfig(request.Context(), cfg, machine)
		if err != nil {
			http.Error(response, "Failed to fetch machine config", http.StatusInternalServerError)

			return
		}

		response.Header().Set("Content-Type", "application/x-yaml")

		_, err = response.Write([]byte("#!talos\n"))
		if err != nil {
			http.Error(response, "Failed to write talos header", http.StatusInternalServerError)

			return
		}

		err = yaml.NewEncoder(response).Encode(machineConfig)
		if err != nil {
			http.Error(response, "Failed to encode machine config", http.StatusInternalServerError)

			return
		}
	}
}

func isTrusted(ctx context.Context, ip string, cfg *config.KommodityConfig) (bool, error) {
	trustEndpoint := strings.Replace(attestation.AttestationTrustEndpoint, "{ip}", ip, 1)
	trustURL := fmt.Sprintf("http://localhost:%d%s", cfg.ServerPort, trustEndpoint)

	client := &http.Client{}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trustURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create trust request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call trust endpoint: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	default:
		return false, restutils.ErrUnexpectedResponse
	}
}

func fetchMachineConfig(ctx context.Context, cfg *config.KommodityConfig,
	machine *clusterv1.Machine) (*v1alpha1.Config, error) {
	kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	secretName := *machine.Spec.Bootstrap.DataSecretName
	secretAPI := kubeClient.CoreV1().Secrets(config.KommodityNamespace)

	secret, err := secretAPI.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	var machineConfig v1alpha1.Config

	err = yaml.Unmarshal(secret.Data["value"], &machineConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal machine config: %w", err)
	}

	return &machineConfig, nil
}
