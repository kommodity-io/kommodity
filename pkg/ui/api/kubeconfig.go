// Package api provides HTTP API handlers for the Kommodity UI.
package api

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/kommodity-io/kommodity/pkg/config"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	talosresconfig "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

//go:embed clusterconfig.tmpl
var clusterConfigFS embed.FS

//go:embed kommodityconfig.tmpl
var kommodityConfigFS embed.FS

type oidcKubeConfig struct {
	*api.Config
	config.OIDCConfig

	BaseURL string
}

func (o *oidcKubeConfig) writeResponse(response http.ResponseWriter, templateFS embed.FS, templateName string) {
	funcs := sprig.FuncMap()
	funcs["b64encBytes"] = func(b []byte) string {
		return base64.StdEncoding.EncodeToString(b)
	}

	tpl, err := template.New("kubeconfig").
		Funcs(funcs).
		ParseFS(templateFS, templateName)
	if err != nil {
		http.Error(response, fmt.Sprintf("Failed to parse kubeconfig template: %v", err), http.StatusInternalServerError)

		return
	}

	response.Header().Set("Content-Type", "application/x-yaml")
	response.WriteHeader(http.StatusOK)

	err = tpl.ExecuteTemplate(response, templateName, o)
	if err != nil {
		fmt.Printf("Failed to execute kubeconfig template: %v\n", err)
	}
}

// GetKommodityKubeConfig handles requests for retrieving the Kommodity kubeconfig.
func GetKommodityKubeConfig(cfg *config.KommodityConfig) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		(&oidcKubeConfig{
			BaseURL:    cfg.BaseURL,
			Config:     nil,
			OIDCConfig: *cfg.AuthConfig.OIDCConfig,
		}).writeResponse(response, kommodityConfigFS, "kommodityconfig.tmpl")
	}
}

// GetKubeConfig handles requests for retrieving the kubeconfig for a given cluster.
//
//nolint:funlen
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

		kubeConfig, err := clientcmd.Load(kubeConfigBytes)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to load kubeconfig: %v", err),
				http.StatusInternalServerError)

			return
		}

		// Fetch OIDC config from the downstream Talos cluster's machine config
		oidcConfig, err := getOIDCConfigFromCluster(request.Context(), clusterName, talosConfig)
		if err != nil {
			if errors.Is(err, ErrOIDCNotConfigured) {
				http.Error(response, "Cluster does not have OIDC configured in apiServer.extraArgs",
					http.StatusForbidden)

				return
			}

			http.Error(response, fmt.Sprintf("Failed to get OIDC config from cluster: %v", err),
				http.StatusInternalServerError)

			return
		}

		// Override admin user kubeconfig with OIDC settings from the cluster.
		(&oidcKubeConfig{
			BaseURL:    cfg.BaseURL,
			Config:     kubeConfig,
			OIDCConfig: *oidcConfig,
		}).writeResponse(response, clusterConfigFS, "clusterconfig.tmpl")
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

func getTalosClientWithNodeCtx(ctx context.Context, clusterName string,
	talosConfig *talosconfig.Config) (*talosclient.Client, context.Context, error) {
	talosClient, err := talosclient.New(ctx, talosclient.WithConfig(talosConfig))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create talos client: %w", err)
	}

	talosClusterContext, success := talosConfig.Contexts[clusterName]
	if !success {
		return nil, nil, ErrFailedToFindContext
	}

	nodeCtx := talosclient.WithNode(ctx, talosClusterContext.Endpoints[0])

	return talosClient, nodeCtx, nil
}

func getKubeContext(ctx context.Context, clusterName string, talosConfig *talosconfig.Config) ([]byte, error) {
	talosClient, nodeCtx, err := getTalosClientWithNodeCtx(ctx, clusterName, talosConfig)
	if err != nil {
		return nil, err
	}

	kubeconfig, err := talosClient.Kubeconfig(nodeCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	return kubeconfig, nil
}

// getOIDCConfigFromCluster fetches the machine config from the downstream Talos cluster
// and extracts OIDC configuration from cluster.apiServer.extraArgs.
//
//nolint:cyclop,funlen
func getOIDCConfigFromCluster(ctx context.Context, clusterName string,
	talosConfig *talosconfig.Config) (*config.OIDCConfig, error) {
	talosClient, nodeCtx, err := getTalosClientWithNodeCtx(ctx, clusterName, talosConfig)
	if err != nil {
		return nil, err
	}

	// Get the MachineConfig resource from the downstream cluster
	machineConfig, err := safe.StateGet[*talosresconfig.MachineConfig](
		nodeCtx,
		talosClient.COSI,
		resource.NewMetadata(
			talosresconfig.NamespaceName,
			talosresconfig.MachineConfigType,
			talosresconfig.ActiveID,
			resource.VersionUndefined),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %w", err)
	}

	// Extract OIDC settings from cluster.apiServer.extraArgs
	cfg := machineConfig.Config()
	if cfg.Cluster() == nil || cfg.Cluster().APIServer() == nil {
		return nil, ErrOIDCNotConfigured
	}

	extraArgs := cfg.Cluster().APIServer().ExtraArgs()
	if extraArgs == nil {
		return nil, ErrOIDCNotConfigured
	}

	// Check if OIDC is configured
	issuerURL, hasIssuer := extraArgs["oidc-issuer-url"]
	clientID, hasClientID := extraArgs["oidc-client-id"]

	if !hasIssuer || !hasClientID {
		return nil, ErrOIDCNotConfigured
	}

	oidcConfig := &config.OIDCConfig{
		IssuerURL: issuerURL,
		ClientID:  clientID,
	}

	if usernameClaim, ok := extraArgs["oidc-username-claim"]; ok {
		oidcConfig.UsernameClaim = usernameClaim
	}

	if groupsClaim, ok := extraArgs["oidc-groups-claim"]; ok {
		oidcConfig.GroupsClaim = groupsClaim
	}

	// Handle extra scopes - they may be comma-separated or multiple entries
	if extraScope, ok := extraArgs["oidc-extra-scope"]; ok {
		// Split by comma in case multiple scopes are in one string
		scopes := strings.Split(extraScope, ",")

		trimmedScopes := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			trimmedScopes = append(trimmedScopes, strings.TrimSpace(scope))
		}

		oidcConfig.ExtraScopes = trimmedScopes
	}

	return oidcConfig, nil
}
