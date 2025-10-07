package server

import (
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kine"
	"github.com/kommodity-io/kommodity/pkg/provider"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/util/webhook"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
)

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
	var schemeGroupVersions = append(
		provider.GetProviderGroupKindVersions(),
		getSupportedGroupKindVersions()...)

	kineStorageConfig, err := kine.NewKineStorageConfig(cfg,
		codecs.CodecForVersions(
			codecs.LegacyCodec(schemeGroupVersions...),
			codecs.UniversalDeserializer(),
			schema.GroupVersions(schemeGroupVersions),
			schema.GroupVersions(schemeGroupVersions),
		))
	if err != nil {
		return nil, fmt.Errorf("unable to create Kine legacy storage config: %w", err)
	}

	restOptions := kine.NewKineRESTOptionsGetter(*kineStorageConfig)

	// Make sure that the API Legacy server and the Extension server are running with same configs
	crdRecommended := genericapiserver.NewRecommendedConfig(codecs)
	crdRecommended.SecureServing = genericServerConfig.SecureServing
	crdRecommended.Authentication = genericServerConfig.Authentication
	crdRecommended.Authorization = genericServerConfig.Authorization
	crdRecommended.LoopbackClientConfig = genericServerConfig.LoopbackClientConfig
	crdRecommended.EffectiveVersion = genericServerConfig.EffectiveVersion
	crdRecommended.OpenAPIV3Config = genericServerConfig.OpenAPIV3Config
	crdRecommended.EquivalentResourceRegistry = genericServerConfig.EquivalentResourceRegistry
	crdRecommended.RESTOptionsGetter = restOptions
	crdRecommended.AggregatedDiscoveryGroupManager = genericServerConfig.AggregatedDiscoveryGroupManager
	crdRecommended.MergedResourceConfig = genericServerConfig.MergedResourceConfig
	crdRecommended.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	crdRecommended.SharedInformerFactory = clientgoinformers.NewSharedInformerFactory(
		clientgoclientset.NewForConfigOrDie(genericServerConfig.LoopbackClientConfig), defaultResyncPeriod*time.Minute)

	return &apiextensionsapiserver.Config{
		GenericConfig: crdRecommended,
		ExtraConfig: apiextensionsapiserver.ExtraConfig{
			CRDRESTOptionsGetter: restOptions,
			ServiceResolver:      webhook.NewDefaultServiceResolver(),
			MasterCount:          1,
		},
	}, nil
}
