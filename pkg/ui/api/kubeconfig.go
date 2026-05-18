// Package api provides HTTP API handlers for the Kommodity UI.
package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/kommodity-io/kommodity/pkg/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
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

func (o *oidcKubeConfig) renderToString(templateFS embed.FS, templateName string) (string, error) {
	var buf bytes.Buffer

	funcs := sprig.FuncMap()
	funcs["b64encBytes"] = func(b []byte) string {
		return base64.StdEncoding.EncodeToString(b)
	}

	tpl, err := template.New("kubeconfig").
		Funcs(funcs).
		ParseFS(templateFS, templateName)
	if err != nil {
		return "", fmt.Errorf("failed to parse kubeconfig template: %w", err)
	}

	err = tpl.ExecuteTemplate(&buf, templateName, o)
	if err != nil {
		return "", fmt.Errorf("failed to execute kubeconfig template: %w", err)
	}

	return buf.String(), nil
}

// GetKommodityKubeConfig returns the Kommodity kubeconfig as a string.
func GetKommodityKubeConfig(cfg *config.KommodityConfig) (string, error) {
	var buf bytes.Buffer

	oidcCfg := &oidcKubeConfig{
		BaseURL:    cfg.BaseURL,
		Config:     nil,
		OIDCConfig: *cfg.AuthConfig.OIDCConfig,
	}

	funcs := sprig.FuncMap()
	funcs["b64encBytes"] = func(b []byte) string {
		return base64.StdEncoding.EncodeToString(b)
	}

	tpl, err := template.New("kubeconfig").
		Funcs(funcs).
		ParseFS(kommodityConfigFS, "kommodityconfig.tmpl")
	if err != nil {
		return "", fmt.Errorf("failed to parse kubeconfig template: %w", err)
	}

	err = tpl.ExecuteTemplate(&buf, "kommodityconfig.tmpl", oidcCfg)
	if err != nil {
		return "", fmt.Errorf("failed to execute kubeconfig template: %w", err)
	}

	return buf.String(), nil
}

// GetClusterKubeconfigContent retrieves the kubeconfig content for a cluster as a string.
// It applies the same logic as the API endpoint: returning raw kubeconfig in dev mode,
// or OIDC-enabled kubeconfig in production.
func GetClusterKubeconfigContent(
	ctx context.Context,
	cfg *config.KommodityConfig,
	clusterName string,
) (string, error) {
	kubeClient, err := clientgoclientset.NewForConfig(cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kube client: %w", err)
	}

	kubeConfigBytes, err := getKubeConfig(ctx, clusterName, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// In development mode, return raw kubeconfig
	if cfg.DevelopmentMode {
		return string(kubeConfigBytes), nil
	}

	// If auth config application is disabled, return error
	if !cfg.AuthConfig.Apply {
		return "", ErrAuthConfigDisabled
	}

	// Load kubeconfig
	kubeConfig, err := clientcmd.Load(kubeConfigBytes)
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Fetch OIDC config from cluster (machine config lives in the cluster's namespace).
	oidcConfig, err := getOIDCConfigFromCluster(ctx, clusterName, clusterName, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to get OIDC config: %w", err)
	}

	// Render OIDC-enabled kubeconfig
	oidcKubeconfig := &oidcKubeConfig{
		BaseURL:    cfg.BaseURL,
		Config:     kubeConfig,
		OIDCConfig: *oidcConfig,
	}

	return oidcKubeconfig.renderToString(clusterConfigFS, "clusterconfig.tmpl")
}

func getKubeConfig(ctx context.Context, clusterName string, kubeClient *clientgoclientset.Clientset) ([]byte, error) {
	secretName := clusterName + "-kubeconfig"

	// CAPI's bootstrap provider creates the kubeconfig secret in the same
	// namespace as the Cluster CR, which (by Kommodity convention) is named
	// after the cluster.
	secretAPI := kubeClient.CoreV1().Secrets(clusterName)

	secret, err := secretAPI.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret: %w", err)
	}

	kubeConfigBytes, ok := secret.Data["value"]
	if !ok || kubeConfigBytes == nil {
		return nil, fmt.Errorf("%w: %s", ErrKubeConfigSecretIsEmpty, secretName)
	}

	return kubeConfigBytes, nil
}

// getOIDCConfigFromCluster fetches the machine config from the downstream Talos cluster
// and extracts OIDC configuration from cluster.apiServer.extraArgs.
//
//nolint:cyclop
func getOIDCConfigFromCluster(
	ctx context.Context,
	clusterName string,
	namespace string,
	kubeClient *clientgoclientset.Clientset,
) (*config.OIDCConfig, error) {
	provider, err := getFirstMachineConfig(ctx, clusterName, namespace, kubeClient)
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
	issuerURL, hasIssuer := firstExtraArg(extraArgs, "oidc-issuer-url")
	clientID, hasClientID := firstExtraArg(extraArgs, "oidc-client-id")

	if !hasIssuer || !hasClientID {
		return nil, ErrOIDCNotConfigured
	}

	oidcConfig := &config.OIDCConfig{
		IssuerURL: issuerURL,
		ClientID:  clientID,
	}

	if usernameClaim, ok := firstExtraArg(extraArgs, "oidc-username-claim"); ok {
		oidcConfig.UsernameClaim = usernameClaim
	}

	if groupsClaim, ok := firstExtraArg(extraArgs, "oidc-groups-claim"); ok {
		oidcConfig.GroupsClaim = groupsClaim
	}

	// Handle extra scopes - they may be comma-separated or multiple entries
	if extraScopes, ok := extraArgs["oidc-extra-scope"]; ok {
		trimmedScopes := make([]string, 0, len(extraScopes))
		for _, entry := range extraScopes {
			for scope := range strings.SplitSeq(entry, ",") {
				trimmed := strings.TrimSpace(scope)
				if trimmed != "" {
					trimmedScopes = append(trimmedScopes, trimmed)
				}
			}
		}

		oidcConfig.ExtraScopes = trimmedScopes
	}

	return oidcConfig, nil
}

func firstExtraArg(extraArgs map[string][]string, key string) (string, bool) {
	values, ok := extraArgs[key]
	if !ok || len(values) == 0 || values[0] == "" {
		return "", false
	}

	return values[0], true
}

func getFirstMachineConfig(
	ctx context.Context,
	clusterName string,
	namespace string,
	kubeClient *clientgoclientset.Clientset,
) (talosconfig.Provider, error) {
	machineConfigList, err := kubeClient.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list machine config secrets: %w", err)
	}

	if len(machineConfigList.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMachineConfigSecret, clusterName)
	}

	sort.Slice(machineConfigList.Items, func(i int, j int) bool {
		return machineConfigList.Items[i].CreationTimestamp.After(machineConfigList.Items[j].CreationTimestamp.Time)
	})

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
