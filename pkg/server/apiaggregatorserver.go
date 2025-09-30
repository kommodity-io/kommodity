package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/controller"
	"github.com/kommodity-io/kommodity/pkg/kine"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/kommodity-io/kommodity/pkg/provider"
	"go.uber.org/zap"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	discoveryendpoint "k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	genericapiserver "k8s.io/apiserver/pkg/server"
	restclientdynamic "k8s.io/client-go/dynamic"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	apiregistrationclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/controllers/autoregister"
	"k8s.io/kubernetes/pkg/controlplane/controller/crdregistration"
)

func newAPIAggregatorServer(genericServerConfig *genericapiserver.RecommendedConfig,
	scheme *runtime.Scheme,
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

	apiServices := registerAPIServicesAndVersions(delegationTarget, config.GenericConfig.AggregatedDiscoveryGroupManager)
	for _, apiService := range apiServices {
		autoRegistrationController.AddAPIServiceToSyncOnStart(apiService)
	}

	crdRegistrationController := crdregistration.NewCRDRegistrationController(
		crds,
		autoRegistrationController)

	//nolint:mnd // Copied from upstream k8s.io/kubernetes/pkg/controlplane/apiserver/aggregator.go
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

	err = aggregatorServer.GenericAPIServer.AddPostStartHook("apply-crds", applyCRDsHook(genericServerConfig, scheme))
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for applying CRDs: %w", err)
	}

	return aggregatorServer, nil
}

func applyCRDsHook(genericServerConfig *genericapiserver.RecommendedConfig,
	scheme *runtime.Scheme) genericapiserver.PostStartHookFunc {
	return func(ctx genericapiserver.PostStartHookContext) error {
		logger := logging.FromContext(ctx)

		dynamicClient, err := restclientdynamic.NewForConfig(genericServerConfig.LoopbackClientConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic rest client: %w", err)
		}

		errCh := make(chan error)

		go func() {
			err = provider.ApplyAllProviders(ctx, dynamicClient)
			if err != nil {
				errCh <- fmt.Errorf("failed to apply all provider CRDs: %w", err)
			} else {
				errCh <- nil
			}
		}()

		err = <-errCh
		if err != nil {
			return fmt.Errorf("error applying provider CRDs: %w", err)
		}

		ctlMgr, err := controller.NewAggregatedControllerManager(ctx, genericServerConfig.LoopbackClientConfig, scheme)
		if err != nil {
			return fmt.Errorf("failed to create controller manager: %w", err)
		}

		go func() {
			runCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)

			err = ctlMgr.Start(runCtx)
			if err != nil {
				errorMsg := "failed to start controller manager:"
				logger.Error(errorMsg, zap.Error(err))
				cancel(fmt.Errorf("%s %w", errorMsg, err))
			}
		}()

		return nil
	}
}

func registerAPIServicesAndVersions(delegationTarget genericapiserver.DelegationTarget,
	discoveryManager discoveryendpoint.ResourceManager) []*apiregistrationv1.APIService {
	apiVersionPriorities := defaultGenericAPIServicePriorities()

	apiServices := make([]*apiregistrationv1.APIService, 0)

	for _, curr := range delegationTarget.ListedPaths() {
		if curr == "/api/v1" {
			apiService := makeAPIService(schema.GroupVersion{Group: "", Version: "v1"}, apiVersionPriorities)
			if apiService == nil {
				continue
			}

			apiServices = append(apiServices, apiService)

			continue
		}

		if !strings.HasPrefix(curr, "/apis/") {
			continue
		}
		// this comes back in a list that looks like /apis/rbac.authorization.k8s.io/v1alpha1
		tokens := strings.Split(curr, "/")
		//nolint:mnd // Copied from upstream k8s.io/kubernetes/pkg/controlplane/apiserver/aggregator.go
		if len(tokens) != 4 {
			continue
		}

		apiService := makeAPIService(schema.GroupVersion{Group: tokens[2], Version: tokens[3]}, apiVersionPriorities)
		if apiService == nil {
			continue
		}

		apiServices = append(apiServices, apiService)
	}

	for gv, entry := range apiVersionPriorities {
		discoveryManager.SetGroupVersionPriority(metav1.GroupVersion(gv),
			int(entry.Group), int(entry.Version))
	}

	return apiServices
}

func setupAPIAggregatorConfig(genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory) (*aggregatorapiserver.Config, error) {
	kineStorageConfig, err := kine.NewKineStorageConfig(
		codecs.CodecForVersions(
			codecs.LegacyCodec(apiregistrationv1.SchemeGroupVersion),
			codecs.UniversalDeserializer(),
			schema.GroupVersions{apiregistrationv1.SchemeGroupVersion},
			runtime.InternalGroupVersioner,
		))
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
		clientgoclientset.NewForConfigOrDie(genericServerConfig.LoopbackClientConfig), defaultResyncPeriod*time.Minute)

	return &aggregatorConfig, nil
}
