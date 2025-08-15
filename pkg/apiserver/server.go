// Package apiserver provides in the implementation of the Kubernetes API Server
package apiserver

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/kine"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"github.com/kommodity-io/kommodity/pkg/storage/namespaces"
	"github.com/kommodity-io/kommodity/pkg/storage/secrets"
	corev1 "k8s.io/api/core/v1"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	apiservercompatibility "k8s.io/apiserver/pkg/util/compatibility"
	restclient "k8s.io/client-go/rest"
)

const defaultAPIServerPort = 8443

// New creates a new Kubernetes API Server.
func New() (*genericapiserver.GenericAPIServer, error) {
	_, err := database.SetupDB()
	if err != nil {
		return nil, fmt.Errorf("failed to setup database connection: %w", err)
	}

	openAPISpec, err := generatedopenapi.NewOpenAPISpec()
	if err != nil {
		return nil, fmt.Errorf("failed to extract desired OpenAPI spec for server: %w", err)
	}

	scheme, codecs, err := newSchemeAndCodec()
	if err != nil {
		return nil, fmt.Errorf("failed to create scheme and codecs: %w", err)
	}

	kineStorageConfig, err := kine.NewKineLegacyStorageConfig(codecs)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	genericServerConfig, err := setupConfig(openAPISpec, scheme, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup config for the generic api server: %w", err)
	}

	apiExtCfg := setupAPIExtensionConfig(genericServerConfig, *kineStorageConfig, codecs)

	// Create the CRD server (delegate target)
	crdServer, err := apiExtCfg.Complete().New(genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to build apiextensions (CRD) server: %w", err)
	}

	// Creates a new API Server with self-signed certs settings
	genericServer, err := genericServerConfig.Complete().New("kommodity-api-server", crdServer.GenericAPIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to build the generic api server: %w", err)
	}

	legacyAPI, err := setupLegacyAPI(scheme, *kineStorageConfig, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to legacy api group info for the generic api server: %w", err)
	}

	err = genericServer.InstallLegacyAPIGroup("/api", legacyAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to install legacy API group into the generic api server: %w", err)
	}

	return genericServer, nil
}

func newSchemeAndCodec() (*runtime.Scheme, *serializer.CodecFactory, error) {
	scheme := runtime.NewScheme()

	err := corev1.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add core v1 API to scheme: %w", err)
	}

	err = apiextensionsv1.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add apiextensions v1 API to scheme: %w", err)
	}

	err = apiextensionsinternal.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add apiextensions internal API to scheme: %w", err)
	}

	codecs := serializer.NewCodecFactory(scheme)

	return scheme, &codecs, nil
}

func setupAPIExtensionConfig(genericServerConfig *genericapiserver.RecommendedConfig,
	kineStorageConfig storagebackend.Config, codecs *serializer.CodecFactory) apiextensionsapiserver.Config {
	restOptions := kine.NewKineRESTOptionsGetter(kineStorageConfig)

	genericServerConfig.MergedResourceConfig = apiextensionsapiserver.DefaultAPIResourceConfigSource()

	// Make sure that the API Legacy server and the Extension server are running with same configs
	crdRecommended := genericapiserver.NewRecommendedConfig(*codecs)
	crdRecommended.SecureServing = genericServerConfig.SecureServing
	crdRecommended.Authentication = genericServerConfig.Authentication
	crdRecommended.Authorization = genericServerConfig.Authorization
	crdRecommended.LoopbackClientConfig = genericServerConfig.LoopbackClientConfig
	crdRecommended.EffectiveVersion = genericServerConfig.EffectiveVersion
	crdRecommended.OpenAPIV3Config = genericServerConfig.OpenAPIV3Config
	crdRecommended.EquivalentResourceRegistry = genericServerConfig.EquivalentResourceRegistry
	crdRecommended.MergedResourceConfig = genericServerConfig.MergedResourceConfig
	crdRecommended.RESTOptionsGetter = restOptions

	return apiextensionsapiserver.Config{
		GenericConfig: crdRecommended,
		ExtraConfig: apiextensionsapiserver.ExtraConfig{
			CRDRESTOptionsGetter: restOptions,
		},
	}
}

func setupConfig(openAPISpec *generatedopenapi.Spec, scheme *runtime.Scheme,
	codecs *serializer.CodecFactory) (*genericapiserver.RecommendedConfig, error) {
	genericServerConfig := genericapiserver.NewRecommendedConfig(*codecs)

	genericServerConfig.EffectiveVersion = apiservercompatibility.DefaultBuildEffectiveVersion()

	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		openAPISpec.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(scheme),
	)

	secureServing, err := setupSecureServingWithSelfSigned()
	if err != nil {
		return nil, fmt.Errorf("failed to setup secure serving config: %w", err)
	}

	err = secureServing.ApplyTo(&genericServerConfig.SecureServing)
	if err != nil {
		return nil, fmt.Errorf("failed to apply secure serving config: %w", err)
	}

	loopbackConfig, err := setupNewLoopbackClientConfig(
		genericServerConfig.SecureServing, secureServing.ServerCert.CertKey)
	if err != nil {
		return nil, fmt.Errorf("failed to setup loopback client config: %w", err)
	}

	genericServerConfig.LoopbackClientConfig = loopbackConfig

	genericServerConfig.EquivalentResourceRegistry = runtime.NewEquivalentResourceRegistry()

	return genericServerConfig, nil
}

func setupSecureServingWithSelfSigned() (*options.SecureServingOptions, error) {
	secureServing := options.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("0.0.0.0")
	secureServing.BindPort = getAPIServerPort()

	// Generate self-signed certs for "localhost"
	alternateIPs := []net.IP{
		net.ParseIP("127.0.0.1"), // IPv4
		net.ParseIP("::1"),       // IPv6
	}
	alternateDNS := []string{"localhost", "apiserver-loopback-client"}

	err := secureServing.MaybeDefaultWithSelfSignedCerts("localhost", alternateDNS, alternateIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to generate self-signed certs: %w", err)
	}

	return secureServing, nil
}

func setupNewLoopbackClientConfig(secureServing *genericapiserver.SecureServingInfo,
	certKey options.CertKey) (*restclient.Config, error) {
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

	return loopbackConfig, nil
}

func setupLegacyAPI(scheme *runtime.Scheme, kineStorageConfig storagebackend.Config,
	codecs *serializer.CodecFactory) (*genericapiserver.APIGroupInfo, error) {
	coreAPIGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(corev1.GroupName, scheme,
		runtime.NewParameterCodec(scheme), *codecs)

	namespacesStorage, err := namespaces.NewNamespacesREST(kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 namespaces: %w", err)
	}

	secretsStorage, err := secrets.NewSecretsREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 secrets: %w", err)
	}

	coreAPIGroupInfo.VersionedResourcesStorageMap["v1"] = map[string]rest.Storage{
		"namespaces": namespacesStorage,
		"secrets":    secretsStorage,
	}

	return &coreAPIGroupInfo, nil
}

func getAPIServerPort() int {
	apiServerPort := os.Getenv("KOMMODITY_API_SERVER_PORT")
	if apiServerPort == "" {
		log.Printf("KOMMODITY_API_SERVER_PORT is not set, defaulting to %d", defaultAPIServerPort)

		return defaultAPIServerPort
	}

	apiServerPortInt, err := strconv.Atoi(apiServerPort)
	if err != nil {
		log.Printf("failed to convert KOMMODITY_API_SERVER_PORT to integer: %v, defaulting to %d", 
			apiServerPort, defaultAPIServerPort)

		return defaultAPIServerPort
	}

	return apiServerPortInt
}
