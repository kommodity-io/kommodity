package libkapi

import (
	"fmt"
	"maps"
	"net"
	"time"

	"github.com/google/uuid"
	apiextensionsopenapi "k8s.io/apiextensions-apiserver/pkg/generated/openapi"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	apiserveropenapi "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	apiserverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/util/feature"
	clientgoinformers "k8s.io/client-go/informers"
	clientgoclientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	componentbaseversion "k8s.io/component-base/version"
	openapicommon "k8s.io/kube-openapi/pkg/common"
	kubernetesopenapi "k8s.io/kubernetes/pkg/generated/openapi"
)

// sharedInformerResyncPeriod matches pkg/server's own default.
const sharedInformerResyncPeriod = 10 * time.Minute

// setupAPIServerConfig builds the generic apiserver config shared by the
// standard-API delegate, the CRD server, and the aggregator.
//
// It deliberately never sets SecureServing: k8s.io/apiserver's
// GenericAPIServer only ever opens a listener when SecureServingInfo is
// non-nil (see NonBlockingRunWithContext), so leaving it nil means the
// generic apiserver machinery never binds a socket of its own. Instead,
// ExternalAddress is set explicitly, which is enough to satisfy
// Config.Complete()'s only fatal check (it requires SecureServing solely to
// derive a port when ExternalAddress itself has none - see
// k8s.io/apiserver@v0.32.6 pkg/server/config.go:699-709). The resulting
// server's Handler is mounted directly on the caller's own http.Server by
// server.go, with no internal TLS hop or reverse proxy.
func setupAPIServerConfig(addr string,
	scheme *runtime.Scheme,
	codecs serializer.CodecFactory,
	groupVersions []schema.GroupVersion,
	authn authenticator.Request,
	authz authorizer.Authorizer,
) (*genericapiserver.RecommendedConfig, error) {
	genericServerConfig := genericapiserver.NewRecommendedConfig(codecs)

	genericServerConfig.ExternalAddress = addr
	genericServerConfig.FeatureGate = feature.DefaultFeatureGate
	genericServerConfig.EquivalentResourceRegistry = runtime.NewEquivalentResourceRegistry()
	genericServerConfig.AggregatedDiscoveryGroupManager = aggregated.NewResourceManager("apis")
	genericServerConfig.EffectiveVersion = componentbaseversion.DefaultBuildEffectiveVersion()
	// Both apiextensions-apiserver and the standard-API storage providers
	// require a non-nil OpenAPIV3Config with real model definitions for their
	// own types - not just to serve /openapi/v3, but internally for CRD
	// structural-schema validation and REST installation. Merge the upstream
	// generated definitions for the standard Kubernetes types with
	// apiextensions-apiserver's own.
	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		mergedOpenAPIDefinitions, apiserveropenapi.NewDefinitionNamer(scheme))

	loopbackConfig, err := newLoopbackClientConfig(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to build loopback client config: %w", err)
	}

	genericServerConfig.LoopbackClientConfig = loopbackConfig

	// The aggregator's remote-availability controller (and its own
	// post-start hook) unconditionally dereferences SharedInformerFactory -
	// it must be a real, working client, not left nil.
	kubeClient, err := clientgoclientset.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback kube client: %w", err)
	}

	genericServerConfig.SharedInformerFactory = clientgoinformers.NewSharedInformerFactory(
		kubeClient, sharedInformerResyncPeriod)

	resourceConfig := apiserverstorage.NewResourceConfig()
	resourceConfig.EnableVersions(groupVersions...)
	genericServerConfig.MergedResourceConfig = resourceConfig

	genericServerConfig.Authentication.Authenticator = authn
	genericServerConfig.Authorization.Authorizer = authz

	return genericServerConfig, nil
}

// newDelegateConfig builds a fresh RecommendedConfig for a delegate server
// (the CRD server, the aggregator) that mirrors every field parent shares
// with it. Both delegates need their own RecommendedConfig - each is a
// distinct genericapiserver.Config - but all of them must agree on the
// fields that make direct-mount serving and shared storage/auth work
// (ExternalAddress in particular: see setupAPIServerConfig's doc comment).
// The caller sets whatever else is specific to its delegate (RESTOptionsGetter,
// AdmissionControl, SkipOpenAPIInstallation, FeatureGate, ...).
func newDelegateConfig(
	parent *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
) *genericapiserver.RecommendedConfig {
	delegate := genericapiserver.NewRecommendedConfig(codecs)
	delegate.ExternalAddress = parent.ExternalAddress
	delegate.SecureServing = parent.SecureServing
	delegate.Authentication = parent.Authentication
	delegate.Authorization = parent.Authorization
	delegate.LoopbackClientConfig = parent.LoopbackClientConfig
	delegate.EffectiveVersion = parent.EffectiveVersion
	delegate.OpenAPIV3Config = parent.OpenAPIV3Config
	delegate.EquivalentResourceRegistry = parent.EquivalentResourceRegistry
	delegate.AggregatedDiscoveryGroupManager = parent.AggregatedDiscoveryGroupManager
	delegate.MergedResourceConfig = parent.MergedResourceConfig
	delegate.BuildHandlerChainFunc = genericapiserver.BuildHandlerChainWithStorageVersionPrecondition
	delegate.SharedInformerFactory = parent.SharedInformerFactory

	return delegate
}

// mergedOpenAPIDefinitions combines the generated OpenAPI definitions for the
// standard Kubernetes types (k8s.io/kubernetes/pkg/generated/openapi) with
// apiextensions-apiserver's own (k8s.io/apiextensions-apiserver/pkg/generated/openapi).
func mergedOpenAPIDefinitions(ref openapicommon.ReferenceCallback) map[string]openapicommon.OpenAPIDefinition {
	defs := kubernetesopenapi.GetOpenAPIDefinitions(ref)
	maps.Copy(defs, apiextensionsopenapi.GetOpenAPIDefinitions(ref))

	return defs
}

// newLoopbackClientConfig builds a plain-HTTP loopback client config for addr,
// carrying a random bearer token that identifies the client as the server
// itself.
//
// The token isn't checked here: each delegate's own genericapiserver.Config
// (built by newDelegateConfig) independently calls AuthorizeClientBearerToken
// during its own Config.Complete() (see server.go, apiextensionsserver.go,
// apiaggregatorserver.go). That upstream helper is a no-op on an empty token
// (k8s.io/apiserver@v0.32.6 pkg/server/config.go:1159-1184); with a non-empty
// one, it unions in a token authenticator that maps this token to
// system:masters. Every internal consumer of LoopbackClientConfig — the
// aggregator's and CRD server's own informers/post-start hooks included —
// authenticates as system:anonymous otherwise, so with an authorizer that
// denies anonymous requests (e.g. AdminAuthorizer), those internal clients
// would be denied along with everyone else. AdminAuthorizer already allows
// system:masters, so no authorizer changes are needed to make this work; a
// caller-supplied WithAuthorizer that doesn't allow system:masters would
// still deny the loopback client, matching plain kube-apiserver's contract.
//
// If the server is bound to an unspecified address (0.0.0.0, [::], or empty),
// the loopback client is pointed at 127.0.0.1 instead, since connecting to an
// unspecified address fails on most platforms and would break the aggregator
// and CRD controllers that rely on LoopbackClientConfig.
func newLoopbackClientConfig(addr string) (*restclient.Config, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to split listener address %q into host and port: %w", addr, err)
	}

	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}

	return &restclient.Config{
		Host:        "http://" + net.JoinHostPort(host, port),
		BearerToken: uuid.New().String(),
	}, nil
}
