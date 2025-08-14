package apiserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"

	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/kine"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"github.com/kommodity-io/kommodity/pkg/storage/namespaces"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	apiservercompatibility "k8s.io/apiserver/pkg/util/compatibility"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
)

// New creates a new Kubernetes API Server.
func New(ctx context.Context) (*genericapiserver.GenericAPIServer, error) {
	_, err := database.SetupDB()
	if err != nil {
		return nil, fmt.Errorf("failed to setup database connection: %w", err)
	}

	openAPISpec, err := generatedopenapi.NewOpenAPISpec()
	if err != nil {
		return nil, fmt.Errorf("failed to extract desired OpenAPI spec for server: %w", err)
	}

	err = corev1.AddToScheme(Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add core v1 API to scheme: %w", err)
	}

	codecs := serializer.NewCodecFactory(Scheme)

	genericServerConfig, err := setupConfig(openAPISpec, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup config for the generic api server: %w", err)
	}

	// Creates a new API Server with self-signed certs settings
	genericServer, err := genericServerConfig.Complete().New("kommodity-api-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to build the generic api server: %w", err)
	}

	legacyAPI, err := setupLegacyAPI(codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to legacy api group info for the generic api server: %w", err)
	}

	if err := genericServer.InstallLegacyAPIGroup("/api", legacyAPI); err != nil {
		return nil, fmt.Errorf("failed to install legacy API group into the generic api server: %w", err)
	}

	return genericServer, nil
}

func setupConfig(openAPISpec *generatedopenapi.Spec, codecs serializer.CodecFactory) (*genericapiserver.RecommendedConfig, error) {
	secureServing := options.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("0.0.0.0")
	secureServing.BindPort = 8443

	genericServerConfig := genericapiserver.NewRecommendedConfig(codecs)

	genericServerConfig.EffectiveVersion = apiservercompatibility.DefaultBuildEffectiveVersion()

	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		openAPISpec.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(Scheme),
	)

	// Generate self-signed certs for "localhost"
	if err := secureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("failed to generate self-signed certs: %w", err)
	}

	if err := secureServing.ApplyTo(&genericServerConfig.SecureServing); err != nil {
		return nil, fmt.Errorf("failed to apply secure serving config: %w", err)
	}

	// Generate a random loopback token
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate loopback token: %w", err)
	}

	loopbackToken := base64.StdEncoding.EncodeToString(tokenBytes)

	// Read cert PEM bytes from generated file
	certPEM, err := os.ReadFile(secureServing.ServerCert.CertKey.CertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert file: %w", err)
	}

	// Create loopback client config
	loopbackConfig, err := genericServerConfig.SecureServing.NewLoopbackClientConfig(loopbackToken, certPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback client config: %w", err)
	}

	genericServerConfig.LoopbackClientConfig = loopbackConfig

	genericServerConfig.EquivalentResourceRegistry = runtime.NewEquivalentResourceRegistry()

	return genericServerConfig, nil
}

func setupLegacyAPI(codecs serializer.CodecFactory) (*genericapiserver.APIGroupInfo, error) {
	coreAPIGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(corev1.GroupName, Scheme,
		runtime.NewParameterCodec(Scheme), codecs)

	kineStorageConfig, err := kine.NewKineLegacyStorageConfig(codecs)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	namespacesStorage, err := namespaces.NewNamespacesREST(*kineStorageConfig, *Scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 namespaces: %w", err)
	}

	coreAPIGroupInfo.VersionedResourcesStorageMap["v1"] = map[string]rest.Storage{
		"namespaces": namespacesStorage,
	}

	return &coreAPIGroupInfo, nil
}
