package libkapi

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	genericapiserver "k8s.io/apiserver/pkg/server"
	webhookutil "k8s.io/apiserver/pkg/util/webhook"

	"github.com/kommodity-io/kommodity/pkg/libkapi/storage"
)

// dispatchingRESTOptionsGetter routes apiextensions.k8s.io/* storage to a
// dedicated getter (crd) and every actual custom resource to another (cr),
// exactly like pkg/server/apiextensionsserver.go's getter of the same name.
type dispatchingRESTOptionsGetter struct {
	crd genericregistry.RESTOptionsGetter
	cr  genericregistry.RESTOptionsGetter
}

func (d dispatchingRESTOptionsGetter) GetRESTOptions(resource schema.GroupResource,
	example runtime.Object) (genericregistry.RESTOptions, error) {
	if resource.Group == apiextensionsv1.GroupName {
		//nolint:wrapcheck // passthrough
		return d.crd.GetRESTOptions(resource, example)
	}

	//nolint:wrapcheck // passthrough
	return d.cr.GetRESTOptions(resource, example)
}

// newAPIExtensionServer builds the apiextensions (CRD) delegate server.
func newAPIExtensionServer(
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	storageEndpoints []string,
	delegationTarget genericapiserver.DelegationTarget,
) (*apiextensionsapiserver.CustomResourceDefinitions, error) {
	config := setupAPIExtensionConfig(genericServerConfig, codecs, storageEndpoints)

	server, err := config.Complete().New(delegationTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to build apiextensions (CRD) server: %w", err)
	}

	return server, nil
}

func setupAPIExtensionConfig(
	genericServerConfig *genericapiserver.RecommendedConfig,
	codecs serializer.CodecFactory,
	storageEndpoints []string,
) *apiextensionsapiserver.Config {
	noConv := serializer.WithoutConversionCodecFactory{CodecFactory: codecs}

	crdROG := storage.NewRESTOptionsGetter(noConv.LegacyCodec(apiextensionsv1.SchemeGroupVersion), storageEndpoints)
	crROG := storage.NewRESTOptionsGetter(unstructured.UnstructuredJSONScheme, storageEndpoints)

	restOptionsGetter := dispatchingRESTOptionsGetter{crd: crdROG, cr: crROG}

	crdRecommended := newDelegateConfig(genericServerConfig, codecs)
	crdRecommended.RESTOptionsGetter = restOptionsGetter
	crdRecommended.AdmissionControl = genericServerConfig.AdmissionControl

	return &apiextensionsapiserver.Config{
		GenericConfig: crdRecommended,
		ExtraConfig: apiextensionsapiserver.ExtraConfig{
			CRDRESTOptionsGetter: restOptionsGetter,
			ServiceResolver:      webhookutil.NewDefaultServiceResolver(),
			AuthResolverWrapper: webhookutil.NewDefaultAuthenticationInfoResolverWrapper(
				nil, nil, crdRecommended.LoopbackClientConfig, nil),
			MasterCount: 1,
		},
	}
}
