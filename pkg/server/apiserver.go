package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	apiserverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/util/feature"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	componentbaseversion "k8s.io/component-base/version"
)

//nolint:funlen // No complexity and readability is fine.
func setupAPIServerConfig(ctx context.Context,
	cfg *config.KommodityConfig,
	openAPISpec *generatedopenapi.Spec,
	scheme *runtime.Scheme,
	codecs serializer.CodecFactory) (*genericapiserver.RecommendedConfig, error) {
	genericServerConfig := genericapiserver.NewRecommendedConfig(codecs)

	genericServerConfig.FeatureGate = feature.DefaultFeatureGate
	genericServerConfig.EquivalentResourceRegistry = runtime.NewEquivalentResourceRegistry()
	genericServerConfig.AggregatedDiscoveryGroupManager = aggregated.NewResourceManager("apis")
	genericServerConfig.EffectiveVersion = componentbaseversion.DefaultBuildEffectiveVersion()
	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		openAPISpec.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(scheme),
	)

	policyEvaluator, err := loadPolicyRuleEvaluator(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load audit policy rule evaluator: %w", err)
	}

	if policyEvaluator != nil {
		genericServerConfig.AuditBackend = getPolicyBackend(ctx)
		genericServerConfig.AuditPolicyRuleEvaluator = policyEvaluator
	}

	secureServing, err := setupSecureServingWithSelfSigned(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup secure serving config: %w", err)
	}

	err = secureServing.ApplyTo(&genericServerConfig.SecureServing)
	if err != nil {
		return nil, fmt.Errorf("failed to apply secure serving config: %w", err)
	}

	loopbackConfig, err := setupNewLoopbackClientConfig(
		genericServerConfig.SecureServing, secureServing.ServerCert.CertKey, cfg.APIServerPort)
	if err != nil {
		return nil, fmt.Errorf("failed to setup loopback client config: %w", err)
	}

	cfg.ClientConfig.LoopbackClientConfig = loopbackConfig

	genericServerConfig.LoopbackClientConfig = loopbackConfig

	resourceConfig := apiserverstorage.NewResourceConfig()
	resourceConfig.EnableVersions(getSupportedGroupKindVersions()...)
	genericServerConfig.MergedResourceConfig = resourceConfig

	err = applyAuth(ctx, cfg, genericServerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to apply authentication/authorization config: %w", err)
	}

	kubeClient, err := clientgoclientset.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	genericServerConfig.SharedInformerFactory = clientgoinformers.NewSharedInformerFactory(
		kubeClient, defaultResyncPeriod*time.Minute)

	err = applyAdmission(genericServerConfig, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to apply admission config: %w", err)
	}

	return genericServerConfig, nil
}

func setupNewLoopbackClientConfig(secureServing *genericapiserver.SecureServingInfo,
	certKey options.CertKey, port int) (*restclient.Config, error) {
	// Generate a random loopback token
	//nolint:mnd
	tokenBytes := make([]byte, 16)

	_, err := rand.Read(tokenBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to generate loopback token: %w", err)
	}

	loopbackToken := base64.StdEncoding.EncodeToString(tokenBytes)

	// Read cert PEM bytes from generated file
	certPEM, err := os.ReadFile(certKey.CertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert file: %w", err)
	}

	// Create loopback client config
	loopbackConfig, err := secureServing.NewLoopbackClientConfig(loopbackToken, certPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback client config: %w", err)
	}

	// Force IPv4 loopback address. The Kubernetes library may choose IPv6 (::1) which
	// doesn't work in some environments like Azure Container Apps that don't support IPv6.
	loopbackConfig.Host = fmt.Sprintf("https://127.0.0.1:%d", port)

	return loopbackConfig, nil
}
