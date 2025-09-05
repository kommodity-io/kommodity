// Package server provides in the implementation of the Kubernetes API Server
package server

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/kine"
	"github.com/kommodity-io/kommodity/pkg/logging"
	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	"github.com/kommodity-io/kommodity/pkg/storage/configmaps"
	"github.com/kommodity-io/kommodity/pkg/storage/endpoints"
	"github.com/kommodity-io/kommodity/pkg/storage/events"
	"github.com/kommodity-io/kommodity/pkg/storage/namespaces"
	"github.com/kommodity-io/kommodity/pkg/storage/secrets"
	"github.com/kommodity-io/kommodity/pkg/storage/services"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"

	// Used to register the API schemes to force init() to be called.
	_ "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/scheme"

	//nolint:staticcheck // Used to register the API schemes to force init() to be called.
	_ "k8s.io/apiextensions-apiserver/pkg/apiserver"
	//nolint:staticcheck
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
)

const (
	defaultResyncPeriod = 10 // in minutes
)

// New creates a new Kubernetes API Server.
//
//nolint:cyclop, funlen // Too long or too complex due to many error checks and setup steps, no real complexity here
func New(ctx context.Context) (*aggregatorapiserver.APIAggregator, error) {
	logger := logging.FromContext(ctx)

	_, err := database.SetupDB()
	if err != nil {
		return nil, fmt.Errorf("failed to setup database connection: %w", err)
	}

	logger.Info("Setting up Open API Specs")

	openAPISpec, err := generatedopenapi.NewOpenAPISpec()
	if err != nil {
		return nil, fmt.Errorf("failed to extract desired OpenAPI spec for server: %w", err)
	}

	err = enhanceScheme(clientgoscheme.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to enhance client-go/kubernetes/scheme scheme: %w", err)
	}

	err = enhanceScheme(apiextensionsapiserver.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to enhance apiserver scheme: %w", err)
	}

	codecs := serializer.NewCodecFactory(clientgoscheme.Scheme)

	genericServerConfig, err := setupAPIServerConfig(ctx, openAPISpec, clientgoscheme.Scheme, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup config for the generic api server: %w", err)
	}

	crdServer, err := newAPIExtensionServer(genericServerConfig, codecs, genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to create apiextensions (CRD) server: %w", err)
	}

	// Creates a new API Server with self-signed certs settings
	genericServer, err := genericServerConfig.Complete().New("kommodity-api-server", crdServer.GenericAPIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to build the generic api server: %w", err)
	}

	logger.Info("Setting up legacy API")

	legacyAPI, err := setupLegacyAPI(clientgoscheme.Scheme, codecs, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to setup legacy API group info for the generic API server: %w", err)
	}

	logger.Info("Installing legacy API group")

	err = genericServer.InstallLegacyAPIGroup("/api", legacyAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to install legacy API group into the generic API server: %w", err)
	}

	logger.Info("Creating new API aggregator server")
	//nolint:contextcheck // No need to pass context here as its not used in the function call
	aggregatorServer, err := newAPIAggregatorServer(
		genericServerConfig,
		codecs,
		genericServer,
		crdServer.Informers.Apiextensions().V1().CustomResourceDefinitions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup API aggregator server: %w", err)
	}

	return aggregatorServer, nil
}

func setupLegacyAPI(
	scheme *runtime.Scheme,
	codecs serializer.CodecFactory,
	logger *zap.Logger,
) (*genericapiserver.APIGroupInfo, error) {
	logger.Info("Creating Kine legacy storage config")

	kineStorageConfig, err := kine.NewKineStorageConfig(codecs.LegacyCodec(corev1.SchemeGroupVersion))
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	coreAPIGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(corev1.GroupName, scheme,
		runtime.NewParameterCodec(scheme), codecs)

	logger.Info("Creating REST storage service for core v1 endpoints")

	endpointsStorage, err := endpoints.NewEndpointsREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 endpoints: %w", err)
	}

	logger.Info("Creating REST storage service for core v1 namespaces")

	namespacesStorage, err := namespaces.NewNamespacesREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 namespaces: %w", err)
	}

	logger.Info("Creating REST storage service for core v1 secrets")

	secretsStorage, err := secrets.NewSecretsREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 secrets: %w", err)
	}

	logger.Info("Creating REST storage service for core v1 services")

	servicesStorage, err := services.NewServicesREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 services: %w", err)
	}

	logger.Info("Creating REST storage service for core v1 configmaps")

	configmapsStorage, err := configmaps.NewConfigMapsREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 configmaps: %w", err)
	}

	eventsStorage, err := events.NewEventsREST(*kineStorageConfig, *scheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST storage service for core v1 events: %w", err)
	}

	coreAPIGroupInfo.VersionedResourcesStorageMap["v1"] = map[string]rest.Storage{
		"endpoints":  endpointsStorage,
		"namespaces": namespacesStorage,
		"services":   servicesStorage,
		"secrets":    secretsStorage,
		"configmaps": configmapsStorage,
		"events":     eventsStorage,
	}

	return &coreAPIGroupInfo, nil
}
