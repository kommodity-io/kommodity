package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/kine"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	apiregistrationclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/controllers/autoregister"
	"k8s.io/kubernetes/pkg/controlplane/controller/crdregistration"
)

func newAPIAggregatorServer(genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	delegationTarget genericapiserver.DelegationTarget,
	crds apiextensionsinformers.CustomResourceDefinitionInformer) (*aggregatorapiserver.APIAggregator, error) {
	config, err := setupAPIAggregatorConfig(genericServerConfig, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup API aggregator config: %w", err)
	}

	aggregatorServer, err := config.Complete().NewWithDelegate(delegationTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to create API aggregator server: %w", err)
	}
	// Create the API Aggregator server config
	apiRegistrationHTTPClient, err := restclient.HTTPClientFor(genericServerConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for API registration: %w", err)
	}

	apiRegistrationRESTClient, err := apiregistrationclient.NewForConfigAndClient(
		genericServerConfig.LoopbackClientConfig, apiRegistrationHTTPClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST client for API registration: %w", err)
	}

	apiRegistrationClient := apiregistrationclient.New(apiRegistrationRESTClient.RESTClient())
	apiServiceInformer := aggregatorServer.APIRegistrationInformers.Apiregistration().V1().APIServices()
	autoRegistrationController := autoregister.NewAutoRegisterController(apiServiceInformer, apiRegistrationClient)

	apiVersionPriorities := defaultGenericAPIServicePriorities()

	for _, curr := range delegationTarget.ListedPaths() {
		if curr == "/api/v1" {
			apiService := makeAPIService(schema.GroupVersion{Group: "", Version: "v1"}, apiVersionPriorities)
			if apiService == nil {
				continue
			}

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
		config.GenericConfig.AggregatedDiscoveryGroupManager.SetGroupVersionPriority(metav1.GroupVersion(gv),
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

func setupAPIAggregatorConfig(genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory) (*aggregatorapiserver.Config, error) {
	kineStorageConfig, err := kine.NewKineStorageConfig(
		codecs.LegacyCodec(apiregistrationv1.SchemeGroupVersion, apiregistration.SchemeGroupVersion))
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	aggregatorConfig := aggregatorapiserver.Config{
		GenericConfig: genericServerConfig,
	}

	aggregatorConfig.GenericConfig.SkipOpenAPIInstallation = true
	aggregatorConfig.GenericConfig.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	aggregatorConfig.GenericConfig.RESTOptionsGetter = kine.NewKineRESTOptionsGetter(*kineStorageConfig)
	aggregatorConfig.GenericConfig.SharedInformerFactory = clientgoinformers.NewSharedInformerFactory(
		clientgoclientset.NewForConfigOrDie(genericServerConfig.LoopbackClientConfig), 10*time.Minute)

	return &aggregatorConfig, nil
}
