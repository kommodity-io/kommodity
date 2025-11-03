// Package api provides HTTP API handlers for the Kommodity UI.
package api

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"

	"github.com/Masterminds/sprig/v3"
	"github.com/kommodity-io/kommodity/pkg/config"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"go.yaml.in/yaml/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

//go:embed oidcaccess.tmpl
var tmplFS embed.FS

type oidcKubeConfig struct {
	*api.Config
	config.OIDCConfig
}

func (o *oidcKubeConfig) writeResponse(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/x-yaml")
	response.WriteHeader(http.StatusOK)

	funcs := sprig.FuncMap()
	funcs["b64encBytes"] = func(b []byte) string {
		return base64.StdEncoding.EncodeToString(b)
	}

	tpl := template.Must(template.New("kubeconfig").Funcs(funcs).ParseFS(tmplFS, "oidcaccess.tmpl"))

	err := tpl.ExecuteTemplate(response, "oidcaccess.tmpl", o)
	if err != nil {
		http.Error(response, fmt.Sprintf("Failed to execute kubeconfig template: %v", err), http.StatusInternalServerError)

		return
	}
}

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

		kubeConfigBytes, err := getKubeContext(request.Context(), clusterName, talosConfig)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to get kubeconfig: %v", err),
				http.StatusInternalServerError)

			return
		}

		if cfg.DevelopmentMode {
			// No need to mask anything in development mode.
			writeResponse(response, kubeConfigBytes)

			return
		}

		if !cfg.AuthConfig.Apply {
			// If auth config application is disabled and not in development mode, do not return kubeconfig,
			http.Error(response, "Auth config application is disabled", http.StatusForbidden)

			return
		}

		config, err := clientcmd.Load(kubeConfigBytes)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to load kubeconfig: %v", err),
				http.StatusInternalServerError)

			return
		}

		// Override admin user kubeconfig with OIDC settings.
		(&oidcKubeConfig{
			Config:     config,
			OIDCConfig: *cfg.AuthConfig.OIDCConfig,
		}).writeResponse(response)
	}
}

func writeResponse(response http.ResponseWriter, data []byte) {
	response.Header().Set("Content-Type", "application/x-yaml")
	response.WriteHeader(http.StatusOK)

	_, err := response.Write(data)
	if err != nil {
		http.Error(response, fmt.Sprintf("Failed to write kubeconfig response: %v", err),
			http.StatusInternalServerError)
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
