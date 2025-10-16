package server

import (
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kine"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	genericapiserver "k8s.io/apiserver/pkg/server"
	webhookutil "k8s.io/apiserver/pkg/util/webhook"
)

// dispatching getter chooses per-group storage codec.
type dispatchingRESTOptionsGetter struct {
	crd genericregistry.RESTOptionsGetter // for apiextensions.k8s.io/*
	cr  genericregistry.RESTOptionsGetter // for all CustomResources
}

func (d dispatchingRESTOptionsGetter) GetRESTOptions(resource schema.GroupResource,
	example runtime.Object) (genericregistry.RESTOptions, error) {
	if resource.Group == apiextensionsv1.GroupName {
		//nolint:wrapcheck // No need to wrap this error as it's just a passthrough.
		return d.crd.GetRESTOptions(resource, example)
	}

	//nolint:wrapcheck // No need to wrap this error as it's just a passthrough.
	return d.cr.GetRESTOptions(resource, example)
}

func newAPIExtensionServer(cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	delegationTarget genericapiserver.DelegationTarget) (*apiextensionsapiserver.CustomResourceDefinitions, error) {
	config, err := setupAPIExtensionConfig(cfg, genericServerConfig, codecs)
	if err != nil {
		return nil, fmt.Errorf("failed to setup API extension config: %w", err)
	}

	server, err := config.Complete().New(delegationTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to build apiextensions (CRD) server: %w", err)
	}

	return server, nil
}

func setupAPIExtensionConfig(cfg *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory) (*apiextensionsapiserver.Config, error) {
	noConv := serializer.WithoutConversionCodecFactory{CodecFactory: codecs}

	crdStorageCfg, err := kine.NewKineStorageConfig(cfg,
		noConv.LegacyCodec(apiextensionsv1.SchemeGroupVersion))
	if err != nil {
		return nil, fmt.Errorf("unable to create CRD Kine storage config: %w", err)
	}

	crdROG := kine.NewKineRESTOptionsGetter(*crdStorageCfg)

	crStorageCfg, err := kine.NewKineStorageConfig(cfg, unstructured.UnstructuredJSONScheme)
	if err != nil {
		return nil, fmt.Errorf("unable to create CR Kine storage config: %w", err)
	}

	crROG := kine.NewKineRESTOptionsGetter(*crStorageCfg)

	restOptionsGetter := dispatchingRESTOptionsGetter{crd: crdROG, cr: crROG}

	// Make sure that the API Legacy server and the Extension server are running with same configs
	crdRecommended := genericapiserver.NewRecommendedConfig(codecs)
	crdRecommended.SecureServing = genericServerConfig.SecureServing
	crdRecommended.Authentication = genericServerConfig.Authentication
	crdRecommended.Authorization = genericServerConfig.Authorization
	crdRecommended.LoopbackClientConfig = genericServerConfig.LoopbackClientConfig
	crdRecommended.EffectiveVersion = genericServerConfig.EffectiveVersion
	crdRecommended.OpenAPIV3Config = genericServerConfig.OpenAPIV3Config
	crdRecommended.EquivalentResourceRegistry = genericServerConfig.EquivalentResourceRegistry
	crdRecommended.RESTOptionsGetter = restOptionsGetter
	crdRecommended.AggregatedDiscoveryGroupManager = genericServerConfig.AggregatedDiscoveryGroupManager
	crdRecommended.MergedResourceConfig = genericServerConfig.MergedResourceConfig
	crdRecommended.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	crdRecommended.SharedInformerFactory = genericServerConfig.SharedInformerFactory

	return &apiextensionsapiserver.Config{
		GenericConfig: crdRecommended,
		ExtraConfig: apiextensionsapiserver.ExtraConfig{
			CRDRESTOptionsGetter: restOptionsGetter,
			ServiceResolver:      webhookutil.NewDefaultServiceResolver(),
			AuthResolverWrapper: webhookutil.NewDefaultAuthenticationInfoResolverWrapper(
				nil, nil, crdRecommended.LoopbackClientConfig, nil),
			MasterCount: 1,
		},
	}, nil
}
