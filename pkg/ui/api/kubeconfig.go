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
	"github.com/kommodity-io/kommodity/pkg/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
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

func (o *oidcKubeConfig) writeResponse(response http.ResponseWriter,
	templateFS embed.FS, templateName string, logger *zap.Logger) {
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
		logger.Error("Failed to execute kubeconfig template", zap.Error(err))
	}
}

// GetKommodityKubeConfig handles requests for retrieving the Kommodity kubeconfig.
func GetKommodityKubeConfig(cfg *config.KommodityConfig, logger *zap.Logger) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		(&oidcKubeConfig{
			BaseURL:    cfg.BaseURL,
			Config:     nil,
			OIDCConfig: *cfg.AuthConfig.OIDCConfig,
		}).writeResponse(response, kommodityConfigFS, "kommodityconfig.tmpl", logger)
	}
}

// GetKubeConfig handles requests for retrieving the kubeconfig for a given cluster.
//
//nolint:funlen
func GetKubeConfig(cfg *config.KommodityConfig, logger *zap.Logger) func(http.ResponseWriter, *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		clusterName := request.PathValue("clusterName")

		kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to create kube client: %v", err),
				http.StatusInternalServerError)
		}

		kubeConfigBytes, err := getKubeConfig(request.Context(), clusterName, kubeClient)
		if err != nil {
			http.Error(response, fmt.Sprintf("Failed to get talosconfig secret: %v", err),
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
		oidcConfig, err := getOIDCConfigFromCluster(request.Context(), clusterName, kubeClient)
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
		}).writeResponse(response, clusterConfigFS, "clusterconfig.tmpl", logger)
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

func getKubeConfig(ctx context.Context, clusterName string, kubeClient *clientgoclientset.Clientset) ([]byte, error) {
	secretName := clusterName + "-kubeconfig"

	secretAPI := kubeClient.CoreV1().Secrets(config.KommodityNamespace)

	secret, err := secretAPI.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret: %w", err)
	}

	return secret.Data["value"], nil
}

// getOIDCConfigFromCluster fetches the machine config from the downstream Talos cluster
// and extracts OIDC configuration from cluster.apiServer.extraArgs.
//
//nolint:cyclop
func getOIDCConfigFromCluster(ctx context.Context, clusterName string,
	kubeClient *clientgoclientset.Clientset) (*config.OIDCConfig, error) {
	provider, err := getFirstMachineConfig(ctx, clusterName, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %w", err)
	}

	// Extract OIDC settings from cluster.apiServer.extraArgs
	if provider.Cluster() == nil || provider.Cluster().APIServer() == nil {
		return nil, ErrOIDCNotConfigured
	}

	extraArgs := provider.Cluster().APIServer().ExtraArgs()
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

func getFirstMachineConfig(ctx context.Context, clusterName string,
	kubeClient *clientgoclientset.Clientset) (talosconfig.Provider, error) {
	machineConfigList, err := kubeClient.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{
		LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list machine config secrets: %w", err)
	}

	if len(machineConfigList.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMachineConfigSecret, clusterName)
	}

	var machineConfigData []byte

	for _, secret := range machineConfigList.Items {
		isControlPlaneBootstrapData := strings.Contains(secret.Name, clusterName+"-controlplane-") &&
			strings.HasSuffix(secret.Name, "-bootstrap-data")
		if isControlPlaneBootstrapData {
			machineConfigData = secret.Data["value"]

			break
		}
	}

	if machineConfigData == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoControlPlaneBootstrapData, clusterName)
	}

	provider, err := configloader.NewFromBytes(machineConfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to load machine config: %w", err)
	}

	return provider, nil
}
