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
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/kine"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"github.com/kommodity-io/kommodity/pkg/storage/endpoints"
	"github.com/kommodity-io/kommodity/pkg/storage/namespaces"
	"github.com/kommodity-io/kommodity/pkg/storage/secrets"
	"github.com/kommodity-io/kommodity/pkg/storage/services"
	corev1 "k8s.io/api/core/v1"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/util/webhook"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	componentbaseversion "k8s.io/component-base/version"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	apiregistrationclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/controllers/autoregister"
	"k8s.io/kubernetes/pkg/controlplane/controller/crdregistration"
)

const defaultAPIServerPort = 8443

// New creates a new Kubernetes API Server.
//
//nolint:cyclop
func New() (*aggregatorapiserver.APIAggregator, error) {
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

	restOptions := kine.NewKineRESTOptionsGetter(*kineStorageConfig)

	apiExtCfg := setupAPIExtensionConfig(genericServerConfig, restOptions, codecs)

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

	crds := crdServer.Informers.Apiextensions().V1().CustomResourceDefinitions()

	aggregatorServer, err := setupAPIAggregatorServer(
		genericServerConfig, crds, genericServer, restOptions, scheme, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup API aggregator server: %w", err)
	}

	return aggregatorServer, nil
}

func newSchemeAndCodec() (*runtime.Scheme, *serializer.CodecFactory, error) {
	scheme := runtime.NewScheme()

	err := corev1.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add core v1 API to scheme: %w", err)
	}

	err = metav1.AddMetaToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add metav1 API to scheme: %w", err)
	}

	err = apiextensionsv1.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add apiextensions v1 API to scheme: %w", err)
	}

	err = apiextensionsinternal.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add apiextensions internal API to scheme: %w", err)
	}

	err = apiregistrationv1.AddToScheme(scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add apiregistration v1 API to scheme: %w", err)
	}

	codecs := serializer.NewCodecFactory(scheme)

	return scheme, &codecs, nil
}

func setupAPIAggregatorServer(genericServerConfig *genericapiserver.RecommendedConfig,
	crds apiextensionsinformers.CustomResourceDefinitionInformer,
	delegationTarget genericapiserver.DelegationTarget,
	restOption generic.RESTOptionsGetter,
	scheme *runtime.Scheme,
	codecs *serializer.CodecFactory) (*aggregatorapiserver.APIAggregator, error) {
	aggregatorConfig := aggregatorapiserver.Config{
		GenericConfig: genericServerConfig,
	}

	resourceConfig := aggregatorapiserver.DefaultAPIResourceConfigSource()
	resourceConfig.EnableVersions(apiregistrationv1.SchemeGroupVersion)
	aggregatorConfig.GenericConfig.MergedResourceConfig = resourceConfig

	aggregatorConfig.GenericConfig.SkipOpenAPIInstallation = true
	aggregatorConfig.GenericConfig.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	aggregatorConfig.GenericConfig.RESTOptionsGetter = restOption
	aggregatorConfig.GenericConfig.SharedInformerFactory = clientgoinformers.NewSharedInformerFactory(
		clientgoclientset.NewForConfigOrDie(genericServerConfig.LoopbackClientConfig), 10*time.Minute)

	aggregatorServer, err := aggregatorConfig.Complete().NewWithDelegate(delegationTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to create API aggregator server: %w", err)
	}
	// Create the API Aggregator server config
	genericServerConfig.LoopbackClientConfig.GroupVersion = &apiregistrationv1.SchemeGroupVersion
	genericServerConfig.LoopbackClientConfig.NegotiatedSerializer = restclient.CodecFactoryForGeneratedClient(
		scheme, *codecs).WithoutConversion()

	apiRegistrationHTTPClient, err := restclient.HTTPClientFor(genericServerConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for API registration: %w", err)
	}

	apiRegistrationRESTClient, err := restclient.RESTClientForConfigAndClient(
		genericServerConfig.LoopbackClientConfig, apiRegistrationHTTPClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST client for API registration: %w", err)
	}

	apiRegistrationClient := apiregistrationclient.New(apiRegistrationRESTClient)
	apiServiceInformer := aggregatorServer.APIRegistrationInformers.Apiregistration().V1().APIServices()
	autoRegistrationController := autoregister.NewAutoRegisterController(apiServiceInformer, apiRegistrationClient)

	apiVersionPriorities := defaultGenericAPIServicePriorities()

	for _, curr := range delegationTarget.ListedPaths() {
		if curr == "/api/v1" {
			apiService := makeAPIService(schema.GroupVersion{Group: "", Version: "v1"}, apiVersionPriorities)
			autoRegistrationController.AddAPIServiceToSyncOnStart(apiService)

			continue
		}

		if !strings.HasPrefix(curr, "/apis/") {
			continue
		}
		// this comes back in a list that looks like /apis/rbac.authorization.k8s.io/v1alpha1
		tokens := strings.Split(curr, "/")
		if len(tokens) != 4 {
			continue
		}

		apiService := makeAPIService(schema.GroupVersion{Group: tokens[2], Version: tokens[3]}, apiVersionPriorities)
		if apiService == nil {
			continue
		}

		autoRegistrationController.AddAPIServiceToSyncOnStart(apiService)
	}

	for gv, entry := range apiVersionPriorities {
		aggregatorConfig.GenericConfig.AggregatedDiscoveryGroupManager.SetGroupVersionPriority(metav1.GroupVersion(gv),
			int(entry.Group), int(entry.Version))
	}

	crdRegistrationController := crdregistration.NewCRDRegistrationController(
		crds,
		autoRegistrationController)

	err = aggregatorServer.GenericAPIServer.AddPostStartHook("kube-apiserver-autoregistration",
		func(context genericapiserver.PostStartHookContext) error {
			go crdRegistrationController.Run(5, context.Done())
			go crdRegistrationController.WaitForInitialSync()
			go autoRegistrationController.Run(5, context.Done())

			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for auto-registration: %w", err)
	}

	return aggregatorServer, nil
}

func setupAPIExtensionConfig(genericServerConfig *genericapiserver.RecommendedConfig,
	restOptions generic.RESTOptionsGetter, codecs *serializer.CodecFactory) apiextensionsapiserver.Config {

	// Make sure that the API Legacy server and the Extension server are running with same configs
	crdRecommended := genericapiserver.NewRecommendedConfig(*codecs)
	crdRecommended.SecureServing = genericServerConfig.SecureServing
	crdRecommended.Authentication = genericServerConfig.Authentication
	crdRecommended.Authorization = genericServerConfig.Authorization
	crdRecommended.LoopbackClientConfig = genericServerConfig.LoopbackClientConfig
	crdRecommended.EffectiveVersion = genericServerConfig.EffectiveVersion
	crdRecommended.OpenAPIV3Config = genericServerConfig.OpenAPIV3Config
	crdRecommended.EquivalentResourceRegistry = genericServerConfig.EquivalentResourceRegistry
	crdRecommended.MergedResourceConfig = apiextensionsapiserver.DefaultAPIResourceConfigSource()
	crdRecommended.RESTOptionsGetter = restOptions

	return apiextensionsapiserver.Config{
		GenericConfig: crdRecommended,
		ExtraConfig: apiextensionsapiserver.ExtraConfig{
			CRDRESTOptionsGetter: restOptions,
			ServiceResolver:      webhook.NewDefaultServiceResolver(),
			MasterCount:          1,
		},
	}
}

func setupConfig(openAPISpec *generatedopenapi.Spec, scheme *runtime.Scheme,
	codecs *serializer.CodecFactory) (*genericapiserver.RecommendedConfig, error) {
	genericServerConfig := genericapiserver.NewRecommendedConfig(*codecs)

	genericServerConfig.EffectiveVersion = componentbaseversion.DefaultBuildEffectiveVersion()

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

	genericServerConfig.AggregatedDiscoveryGroupManager = aggregated.NewResourceManager("apis")

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

	endpointsStorage, err := endpoints.NewEndpointsREST(kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 endpoints: %w", err)
	}

	namespacesStorage, err := namespaces.NewNamespacesREST(kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 namespaces: %w", err)
	}

	secretsStorage, err := secrets.NewSecretsREST(kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 secrets: %w", err)
	}

	servicesStorage, err := services.NewServicesREST(kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 services: %w", err)
	}

	coreAPIGroupInfo.VersionedResourcesStorageMap["v1"] = map[string]rest.Storage{
		"endpoints":  endpointsStorage,
		"namespaces": namespacesStorage,
		"services":   servicesStorage,
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
