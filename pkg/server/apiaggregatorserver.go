package server

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
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
	"k8s.io/client-go/discovery"
	restclientdynamic "k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	apiregistrationclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/controllers/autoregister"
	controllersa "k8s.io/kubernetes/pkg/controller/serviceaccount"
	"k8s.io/kubernetes/pkg/controlplane/controller/crdregistration"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

const (
	defaultWaitTime = 500 * time.Millisecond
	retryInterval   = 30 * time.Second
)

type validatingResources struct {
	providerGroupResources map[string][]string
	providerGroups         []string
}

func (v *validatingResources) hasSameAPIGroups(apiGroupNames []string) bool {
	return ContainsAll(apiGroupNames, v.providerGroups)
}

func (v *validatingResources) hasSameAPIGroupResources(apiGroupResources []*metav1.APIResourceList) bool {
	apiGroupResourcesMap := make(map[string][]string)

	for _, group := range apiGroupResources {
		groupVersion, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return false
		}

		resourceKinds := make([]string, len(group.APIResources))
		for i, resource := range group.APIResources {
			resourceKinds[i] = resource.Kind
		}

		apiGroupResourcesMap[groupVersion.Group] = append(apiGroupResourcesMap[groupVersion.Group], resourceKinds...)
	}

	for group, resources := range v.providerGroupResources {
		apiGroupResources := apiGroupResourcesMap[group]

		if !ContainsAll(apiGroupResources, resources) {
			return false
		}
	}

	return true
}

//nolint:funlen
func newAPIAggregatorServer(cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	providerCache *provider.Cache,
	scheme *runtime.Scheme,
	codecs serializer.CodecFactory,
	delegationTarget genericapiserver.DelegationTarget,
	crds apiextensionsinformers.CustomResourceDefinitionInformer) (*aggregatorapiserver.APIAggregator, error) {
	config, err := setupAPIAggregatorConfig(cfg, genericServerConfig, codecs)
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

	err = aggregatorServer.GenericAPIServer.AddPostStartHook(
		"apply-crds", applyCRDsHook(cfg, genericServerConfig, providerCache, crds))
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for applying CRDs: %w", err)
	}

	err = aggregatorServer.GenericAPIServer.AddPostStartHook(
		"start-controller-managers", startControllerManagersHook(cfg, genericServerConfig, providerCache, scheme))
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for starting controller managers: %w", err)
	}

	err = aggregatorServer.GenericAPIServer.AddPostStartHook(
		"start-token-controller", startTokenControllerHook(genericServerConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for starting token controller: %w", err)
	}

	return aggregatorServer, nil
}

//nolint:funlen // Not possible to shorten this function in a meaningful way due to go routine.
func applyCRDsHook(cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	providerCache *provider.Cache,
	crds apiextensionsinformers.CustomResourceDefinitionInformer) genericapiserver.PostStartHookFunc {
	return func(ctx genericapiserver.PostStartHookContext) error {
		dynamicClient, err := restclientdynamic.NewForConfig(genericServerConfig.LoopbackClientConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic rest client: %w", err)
		}

		errCh := make(chan error)

		go func() {
			webhookURL := fmt.Sprintf("https://localhost:%d", cfg.WebhookPort)

			crt, _, err := getServingCertAndKeyFromFiles(genericServerConfig)
			if err != nil {
				errCh <- fmt.Errorf("failed to get serving PEM from files: %w", err)

				return
			}

			err = providerCache.ApplyCRDProviders(ctx, webhookURL, crt, dynamicClient)
			if err != nil {
				errCh <- fmt.Errorf("failed to apply all provider CRDs: %w", err)

				return
			}

			err = providerCache.ApplyWebhookProviders(ctx, webhookURL, crt, dynamicClient)
			if err != nil {
				errCh <- fmt.Errorf("failed to apply all provider webhooks: %w", err)

				return
			}

			if !cache.WaitForCacheSync(ctx.Done(), crds.Informer().HasSynced) {
				errCh <- ErrTimeoutWaitingForCRD
			} else {
				errCh <- nil
			}

			mwcInf := genericServerConfig.SharedInformerFactory.
				Admissionregistration().V1().MutatingWebhookConfigurations()
			vwcInf := genericServerConfig.SharedInformerFactory.
				Admissionregistration().V1().ValidatingWebhookConfigurations()

			if !cache.WaitForCacheSync(ctx.Done(),
				mwcInf.Informer().HasSynced,
				vwcInf.Informer().HasSynced,
			) {
				errCh <- ErrTimeoutWaitingForWebhook
			} else {
				errCh <- nil
			}
		}()

		err = <-errCh
		if err != nil {
			return fmt.Errorf("error applying provider CRDs: %w", err)
		}

		return nil
	}
}

func startControllerManagersHook(cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	providerCache *provider.Cache,
	scheme *runtime.Scheme) genericapiserver.PostStartHookFunc {
	return func(ctx genericapiserver.PostStartHookContext) error {
		logger := logging.FromContext(ctx)

		discoveryClient, err := discovery.NewDiscoveryClientForConfig(genericServerConfig.LoopbackClientConfig)
		if err != nil {
			return fmt.Errorf("failed to create discovery client: %w", err)
		}

		err = waitForProviderCRDsAreEstablished(ctx, discoveryClient, providerCache)
		if err != nil {
			return fmt.Errorf("failed to waiting for provider CRDs are established: %w", err)
		}

		ctlMgr, err := controller.NewAggregatedControllerManager(ctx, cfg, genericServerConfig, scheme)
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

//nolint:lll // Not possible to shorten the signature
func startTokenControllerHook(genericServerConfig *genericapiserver.RecommendedConfig) genericapiserver.PostStartHookFunc {
	return func(ctx genericapiserver.PostStartHookContext) error {
		kubeClient, err := kubernetes.NewForConfig(genericServerConfig.LoopbackClientConfig)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client for tokens controller: %w", err)
		}

		_, key, err := getServingCertAndKeyFromFiles(genericServerConfig)
		if err != nil {
			return fmt.Errorf("failed to get serving key from files: %w", err)
		}

		block, _ := pem.Decode(key)
		if block == nil {
			return ErrFailedToDecodePEMBlock
		}

		rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			parsedKey, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err2 != nil {
				return fmt.Errorf("%w: %w, %w", ErrFailedToParsePrivateKey, err, err2)
			}

			var success bool

			rsaKey, success = parsedKey.(*rsa.PrivateKey)
			if !success {
				return ErrPrivateKeyNotRSA
			}
		}

		tokenGenerator, err := serviceaccount.JWTTokenGenerator(serviceaccount.LegacyIssuer, rsaKey)
		if err != nil {
			return fmt.Errorf("failed to build token generator: %w", err)
		}

		tokenController, err := controllersa.NewTokensController(
			genericServerConfig.SharedInformerFactory.Core().V1().ServiceAccounts(),
			genericServerConfig.SharedInformerFactory.Core().V1().Secrets(),
			kubeClient,
			controllersa.TokensControllerOptions{
				ServiceAccountResync: retryInterval,
				SecretResync:         retryInterval,
				TokenGenerator:       tokenGenerator,
			})
		if err != nil {
			return fmt.Errorf("failed to create tokens controller: %w", err)
		}

		genericServerConfig.SharedInformerFactory.Start(ctx.Done())

		go func() {
			runCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)

			tokenController.Run(runCtx, 1)
		}()

		return nil
	}
}

func waitForProviderCRDsAreEstablished(ctx context.Context,
	discoveryClient *discovery.DiscoveryClient,
	providerCache *provider.Cache) error {
	logger := logging.FromContext(ctx)

	validator := validatingResources{
		providerGroupResources: providerCache.GetProviderGroupResources(),
		providerGroups:         slices.Collect(maps.Keys(providerCache.GetProviderGroupResources())),
	}

	logger.Info("Waiting for CRD discovery", zap.Strings("apiGroups", validator.providerGroups))

	for {
		apiResources, err := discoveryClient.ServerPreferredResources()
		if err != nil {
			return fmt.Errorf("failed to discover server groups: %w", err)
		}

		apiGroupNames, err := getAPIGroupNamesFromAPIResourceLists(apiResources)
		if err != nil {
			return fmt.Errorf("failed to get API group names from API resource lists: %w", err)
		}

		logger.Info("Discovered API groups", zap.Any("groups", apiGroupNames))

		if validator.hasSameAPIGroups(apiGroupNames) && validator.hasSameAPIGroupResources(apiResources) {
			logger.Info("All provider CRDs are available",
				zap.Strings("expectedGroups", validator.providerGroups),
				zap.Any("discoveredGroups", apiGroupNames))

			break
		}

		logger.Info("Not all provider CRDs are available yet, waiting...",
			zap.Int("expectedCount", len(validator.providerGroups)),
			zap.Strings("expectedGroups", validator.providerGroups),
			zap.Any("discoveredGroups", apiGroupNames))
		time.Sleep(defaultWaitTime)
	}

	return nil
}

func getAPIGroupNamesFromAPIResourceLists(apiGroupResourcesList []*metav1.APIResourceList) ([]string, error) {
	apiGroupNames := make([]string, 0)

	for _, group := range apiGroupResourcesList {
		if group == nil {
			continue
		}

		groupVersion, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to parse group version %q: %w", group.GroupVersion, err)
		}

		if slices.Contains(apiGroupNames, groupVersion.Group) {
			// We could have multiple versions for the same group, skip duplicates
			continue
		}

		apiGroupNames = append(apiGroupNames, groupVersion.Group)
	}

	return apiGroupNames, nil
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

func setupAPIAggregatorConfig(
	cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory) (*aggregatorapiserver.Config, error) {
	noConv := serializer.WithoutConversionCodecFactory{CodecFactory: codecs}

	kineStorageConfig, err := kine.NewKineStorageConfig(cfg,
		noConv.LegacyCodec(apiregistrationv1.SchemeGroupVersion))
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	aggregatorGenericConfig := genericapiserver.NewRecommendedConfig(codecs)
	aggregatorGenericConfig.SecureServing = genericServerConfig.SecureServing
	aggregatorGenericConfig.Authentication = genericServerConfig.Authentication
	aggregatorGenericConfig.Authorization = genericServerConfig.Authorization
	aggregatorGenericConfig.LoopbackClientConfig = genericServerConfig.LoopbackClientConfig
	aggregatorGenericConfig.EffectiveVersion = genericServerConfig.EffectiveVersion
	aggregatorGenericConfig.OpenAPIV3Config = genericServerConfig.OpenAPIV3Config
	aggregatorGenericConfig.EquivalentResourceRegistry = genericServerConfig.EquivalentResourceRegistry
	aggregatorGenericConfig.RESTOptionsGetter = kine.NewKineRESTOptionsGetter(*kineStorageConfig)
	aggregatorGenericConfig.AggregatedDiscoveryGroupManager = genericServerConfig.AggregatedDiscoveryGroupManager
	aggregatorGenericConfig.MergedResourceConfig = genericServerConfig.MergedResourceConfig
	aggregatorGenericConfig.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	aggregatorGenericConfig.SharedInformerFactory = genericServerConfig.SharedInformerFactory
	aggregatorGenericConfig.SkipOpenAPIInstallation = true
	aggregatorGenericConfig.FeatureGate = genericServerConfig.FeatureGate

	return &aggregatorapiserver.Config{
		GenericConfig: aggregatorGenericConfig,
	}, nil
}
