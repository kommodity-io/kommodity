// Package api provides HTTP API handlers for the Kommodity UI.
package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/kommodity-io/kommodity/pkg/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

const kommodityKubeconfigFile = "kommodity.yaml"

func readDevKubeconfig(developmentMode bool) (string, error) {
	if !developmentMode {
		return "", nil
	}

	content, err := os.ReadFile(kommodityKubeconfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("failed to read %s: %w", kommodityKubeconfigFile, err)
	}

	return string(content), nil
}

// GetKommodityKubeConfig returns the Kommodity kubeconfig as a string.
// In development mode without OIDC, falls back to reading kommodity.yaml from the
// working directory. Returns an empty string if neither is available.
func GetKommodityKubeConfig(cfg *config.KommodityConfig) (string, error) {
	if cfg.AuthConfig.OIDCConfig == nil {
		return readDevKubeconfig(cfg.DevelopmentMode)
	}

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

	// Fetch OIDC config from cluster
	oidcConfig, err := getOIDCConfigFromCluster(ctx, clusterName, DefaultNamespace, kubeClient)
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

	secretAPI := kubeClient.CoreV1().Secrets(config.KommodityNamespace)

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

	// Client-side kubelogin flags (e.g. --oidc-extra-scope=...) are not apiserver
	// flags, so they cannot travel via the machine config. They are rendered by the
	// chart into a ConfigMap in the management cluster, labelled with the cluster
	// name, and read here.
	clientExtraFlags, err := getOIDCClientExtraFlags(ctx, clusterName, namespace, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get OIDC client extra flags: %w", err)
	}

	oidcConfig.ClientExtraFlags = clientExtraFlags

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

// oidcClientSecretName is the name of the Secret rendered by the
// kommodity-cluster chart (templates/talos/oidc-client-config.yaml) carrying the
// kubelogin client flags (and possibly a client secret) for the cluster's UI
// kubeconfig. A Secret rather than a ConfigMap because the flags may carry
// sensitive material such as --oidc-client-secret.
const oidcClientSecretName = "-oidc-client"

// getOIDCClientExtraFlags reads the cluster's OIDC client Secret from the
// management cluster and returns the literal kubelogin flags it carries. Returns
// nil (no error) when the Secret is absent — client flags are optional.
func getOIDCClientExtraFlags(
	ctx context.Context,
	clusterName string,
	namespace string,
	kubeClient *clientgoclientset.Clientset,
) ([]string, error) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(
		ctx, clusterName+oidcClientSecretName, metav1.GetOptions{},
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get OIDC client secret %s/%s: %w",
			namespace, clusterName+oidcClientSecretName, err)
	}

	raw, ok := secret.Data["clientExtraFlags"]
	if !ok || len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}

	var flags []string
	if err := json.Unmarshal(raw, &flags); err != nil {
		return nil, fmt.Errorf("failed to parse clientExtraFlags from %s/%s: %w",
			namespace, secret.Name, err)
	}

	return flags, nil
}
