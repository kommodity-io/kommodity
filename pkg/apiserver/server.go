package apiserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/kine"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"github.com/kommodity-io/kommodity/pkg/storage/namespaces"
	"github.com/kommodity-io/kommodity/pkg/storage/secrets"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	apiservercompatibility "k8s.io/apiserver/pkg/util/compatibility"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = apiruntime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs = serializer.NewCodecFactory(Scheme)
)

func New(ctx context.Context) (*genericapiserver.GenericAPIServer, error) {
	database, err := database.SetupDB()
	if err != nil {
		return nil, fmt.Errorf("failed to setup database connection: %v", err)
	}

	openAPISpec, err := generatedopenapi.NewOpenAPISpec()
	if err != nil {
		return nil, fmt.Errorf("failed to extract desired OpenAPI spec for server: %v", err)
	}

	genericServerConfig, err := setupConfig(openAPISpec)
	if err != nil {
		return nil, fmt.Errorf("failed to setup config for the generic api server: %v", err)
	}

	// Creates a new API Server with self-signed certs settings
	genericServer, err := genericServerConfig.Complete().New("kommodity-api-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to build the generic api server: %v", err)
	}

	legacyAPI, err := setupLegacyAPI(database)
	if err != nil {
		return nil, fmt.Errorf("failed to legacy api group info for the generic api server: %v", err)
	}

	if err := genericServer.InstallLegacyAPIGroup("/api", legacyAPI); err != nil {
		return nil, fmt.Errorf("failed to install legacy API group into the generic api server: %v", err)
	}

	return genericServer, nil
}

func setupConfig(openAPISpec *generatedopenapi.OpenAPISpec) (*genericapiserver.RecommendedConfig, error) {
	secureServing := options.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("0.0.0.0")
	secureServing.BindPort = 8443

	genericServerConfig := genericapiserver.NewRecommendedConfig(Codecs)

	genericServerConfig.EffectiveVersion = apiservercompatibility.DefaultBuildEffectiveVersion()

	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		openAPISpec.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(Scheme),
	)

	// Generate self-signed certs for "localhost"
	if err := secureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("failed to generate self-signed certs: %v", err)
	}

	if err := secureServing.ApplyTo(&genericServerConfig.SecureServing); err != nil {
		return nil, fmt.Errorf("failed to apply secure serving config: %v", err)
	}

	// Generate a random loopback token
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate loopback token: %v", err)
	}
	loopbackToken := base64.StdEncoding.EncodeToString(tokenBytes)

	// Read cert PEM bytes from generated file
	certPEM, err := os.ReadFile(secureServing.ServerCert.CertKey.CertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert file: %v", err)
	}

	// Create loopback client config
	loopbackConfig, err := genericServerConfig.SecureServing.NewLoopbackClientConfig(loopbackToken, certPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback client config: %v", err)
	}
	genericServerConfig.LoopbackClientConfig = loopbackConfig

	genericServerConfig.EquivalentResourceRegistry = runtime.NewEquivalentResourceRegistry()

	return genericServerConfig, nil
}

func setupLegacyAPI(database *sqlx.DB) (*genericapiserver.APIGroupInfo, error) {
	corev1.AddToScheme(Scheme)

	coreAPIGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(corev1.GroupName, Scheme,
		runtime.NewParameterCodec(Scheme), Codecs)

	kineStorageConfig, err := kine.NewKineLegacyStorageConfig(database, Codecs)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %v", err)
	}

	namespacesStorage, err := namespaces.NewNamespacesREST(*kineStorageConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 namespaces: %v", err)
	}

	secretsStorage, err := secrets.NewSecretsREST(*kineStorageConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 secrets: %v", err)
	}

	coreAPIGroupInfo.VersionedResourcesStorageMap["v1"] = map[string]rest.Storage{
		"namespaces": namespacesStorage,
		"secrets":    secretsStorage,
	}

	return &coreAPIGroupInfo, nil
}
