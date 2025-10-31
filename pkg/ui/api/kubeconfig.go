// Package api provides HTTP API handlers for the Kommodity UI.
package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ghodss/yaml"
	"github.com/kommodity-io/kommodity/pkg/config"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GetKubeConfig handles requests for retrieving the kubeconfig for a given cluster.
func GetKubeConfig(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		clusterName := request.PathValue("clusterName")

		talosConfig, err := getTalosConfig(request.Context(), clusterName, cfg.ClientConfig.LoopbackClientConfig)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to get talosconfig secret: %v", err),
				http.StatusInternalServerError)

			return
		}

		kubeconfig, err := getKubeContext(request.Context(), clusterName, talosConfig)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to get kubeconfig: %v", err),
				http.StatusInternalServerError)

			return
		}

		response.Header().Set("Content-Type", "application/x-yaml")
		response.WriteHeader(http.StatusOK)

		_, err = response.Write(kubeconfig)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to write kubeconfig response: %v", err),
				http.StatusInternalServerError)

			return
		}
	}
}

func getTalosConfig(ctx context.Context, clusterName string,
	lookbackClientConfig *rest.Config) (*talosconfig.Config, error) {
	kubeClient, err := clientgoclientset.NewForConfig(lookbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	secretName := clusterName + "-talosconfig"

	secretAPI := kubeClient.CoreV1().Secrets(config.KommodityNamespace)

	secret, err := secretAPI.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get talosconfig secret: %w", err)
	}

	var talosConfig *talosconfig.Config

	err = yaml.Unmarshal(secret.Data["talosconfig"], &talosConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal machine config: %w", err)
	}

	return talosConfig, nil
}

func getKubeContext(ctx context.Context, clusterName string, talosConfig *talosconfig.Config) ([]byte, error) {
	talosClient, err := talosclient.New(ctx, talosclient.WithConfig(talosConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to create talos client: %w", err)
	}

	talosClusterContext, success := talosConfig.Contexts[clusterName]
	if !success {
		return nil, ErrFailedToFindContext
	}

	kubeconfig, err := talosClient.Kubeconfig(talosclient.WithNode(ctx, talosClusterContext.Endpoints[0]))
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	return kubeconfig, nil
}
