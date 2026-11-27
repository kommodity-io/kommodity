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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrEmptyMachineConfig is returned when the bootstrap data secret has no "value" key.
var ErrEmptyMachineConfig = errors.New("machine config secret is empty")

// GetUserData godoc
// @Summary  Get user-data for Talos machine config
// @Tags     Metadata
// @Produce  application/x-yaml
// @Success  200  {string}  string   "YAML config for Talos machine config"
// @Failure  404  {object}  string   "If the machine is not found"
// @Failure  500  {object}  string   "If there is a server error"
// @Router   /configs/user-data [get]
//
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

		//nolint:gosec // G705: Data originates from a cluster secret served to a trusted, attested machine.
		_, err = response.Write(machineConfig)
		if err != nil {
			http.Error(response, "Failed to write machine config", http.StatusInternalServerError)

			return
		}
	}
}

func isTrusted(ctx context.Context, ip string, cfg *config.KommodityConfig) (bool, error) {
	trustEndpoint := strings.Replace(attestation.AttestationTrustEndpoint, "{ip}", ip, 1)
	trustURL := fmt.Sprintf("http://localhost:%d%s", cfg.ServerPort, trustEndpoint)

	client := &http.Client{}

	//nolint:gosec // G704: URL is constructed from localhost and config, not user input - SSRF not possible
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trustURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create trust request: %w", err)
	}

	//nolint:gosec // G704: Calling localhost endpoint with internal configuration - SSRF not possible
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
	machine *clusterv1.Machine) ([]byte, error) {
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

	if secret.Data["value"] == nil {
		return nil, fmt.Errorf("%w: %s", ErrEmptyMachineConfig, secretName)
	}

	return secret.Data["value"], nil
}
