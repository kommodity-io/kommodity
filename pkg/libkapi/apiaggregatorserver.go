package libkapi

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	discoveryendpoint "k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	apiregistrationclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/controllers/autoregister"
	"k8s.io/kubernetes/pkg/controlplane/controller/crdregistration"

	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"

	// Used to register the apiregistration API schemes to force init() to be called.
	_ "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/scheme"
)

const bootstrapNamespace = "default"

// newAPIAggregatorServer wraps delegationTarget with the aggregation layer:
// discovery aggregation plus the autoregister/crdregistration controllers
// that keep built-in API groups and CRD-backed groups registered as
// APIServices. Unlike pkg/server/apiaggregatorserver.go, it registers no
// Kommodity-specific post-start hooks (no controller-manager startup, no
// ServiceAccount token controller, no signing-key persistence, no provider
// CRD application) - those are consumers of a Kubernetes-API-compatible
// server, not part of building one.
func newAPIAggregatorServer(
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	storageEndpoints []string,
	delegationTarget genericapiserver.DelegationTarget,
	crds apiextensionsinformers.CustomResourceDefinitionInformer,
) (*aggregatorapiserver.APIAggregator, error) {
	config := setupAPIAggregatorConfig(genericServerConfig, codecs, storageEndpoints)

	aggregatorServer, err := config.Complete().NewWithDelegate(delegationTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to create API aggregator server: %w", err)
	}

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

	crdRegistrationController := crdregistration.NewCRDRegistrationController(crds, autoRegistrationController)

	//nolint:mnd // Matches upstream k8s.io/kubernetes/pkg/controlplane/apiserver/aggregator.go
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
		"bootstrap-default-namespace", bootstrapDefaultNamespaceHook(genericServerConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to add post start hook for bootstrapping the default namespace: %w", err)
	}

	return aggregatorServer, nil
}

// bootstrapDefaultNamespaceHook creates the "default" namespace once the
// server is listening, matching real kube-apiserver's own bootstrapping.
func bootstrapDefaultNamespaceHook(
	genericServerConfig *genericapiserver.RecommendedConfig) genericapiserver.PostStartHookFunc {
	return func(ctx genericapiserver.PostStartHookContext) error {
		kubeClient, err := kubernetes.NewForConfig(genericServerConfig.LoopbackClientConfig)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client for bootstrapping: %w", err)
		}

		_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: bootstrapNamespace},
		}, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %q: %w", bootstrapNamespace, err)
		}

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

func setupAPIAggregatorConfig(
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	storageEndpoints []string,
) *aggregatorapiserver.Config {
	noConv := serializer.WithoutConversionCodecFactory{CodecFactory: codecs}

	aggregatorGenericConfig := newDelegateConfig(genericServerConfig, codecs)
	aggregatorGenericConfig.RESTOptionsGetter = storage.NewRESTOptionsGetter(
		noConv.LegacyCodec(apiregistrationv1.SchemeGroupVersion), storageEndpoints)
	aggregatorGenericConfig.SkipOpenAPIInstallation = true
	aggregatorGenericConfig.FeatureGate = genericServerConfig.FeatureGate

	return &aggregatorapiserver.Config{
		GenericConfig: aggregatorGenericConfig,
	}
}
