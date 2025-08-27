package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	generatedopenapi "github.com/kommodity-io/kommodity/pkg/openapi"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationapiv1 "k8s.io/api/authorization/v1"
	autoscalingapiv1 "k8s.io/api/autoscaling/v1"
	autoscalingapiv2 "k8s.io/api/autoscaling/v2"
	batchapiv1 "k8s.io/api/batch/v1"
	certificatesapiv1 "k8s.io/api/certificates/v1"
	coordinationapiv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	eventsv1 "k8s.io/api/events/v1"
	networkingapiv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyapiv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingapiv1 "k8s.io/api/scheduling/v1"
	storageapiv1 "k8s.io/api/storage/v1"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	apiserverstorage "k8s.io/apiserver/pkg/server/storage"
	restclient "k8s.io/client-go/rest"
	componentbaseversion "k8s.io/component-base/version"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

//nolint:funlen // Too long due to many API versions
func setupAPIServerConfig(ctx context.Context, openAPISpec *generatedopenapi.Spec, scheme *runtime.Scheme,
	codecs serializer.CodecFactory) (*genericapiserver.RecommendedConfig, error) {
	genericServerConfig := genericapiserver.NewRecommendedConfig(codecs)

	genericServerConfig.EffectiveVersion = componentbaseversion.DefaultBuildEffectiveVersion()

	genericServerConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		openAPISpec.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(scheme),
	)

	secureServing, err := setupSecureServingWithSelfSigned(ctx)
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

	resourceConfig := apiserverstorage.NewResourceConfig()
	resourceConfig.EnableVersions(
		corev1.SchemeGroupVersion,
		apiextensionsv1.SchemeGroupVersion,
		apiextensionsinternal.SchemeGroupVersion,
		apiregistrationv1.SchemeGroupVersion,
		apiregistration.SchemeGroupVersion,
		admissionregistrationv1.SchemeGroupVersion,
		corev1.SchemeGroupVersion,
		appsv1.SchemeGroupVersion,
		authenticationv1.SchemeGroupVersion,
		authorizationapiv1.SchemeGroupVersion,
		autoscalingapiv1.SchemeGroupVersion,
		autoscalingapiv2.SchemeGroupVersion,
		batchapiv1.SchemeGroupVersion,
		certificatesapiv1.SchemeGroupVersion,
		coordinationapiv1.SchemeGroupVersion,
		discoveryv1.SchemeGroupVersion,
		eventsv1.SchemeGroupVersion,
		networkingapiv1.SchemeGroupVersion,
		nodev1.SchemeGroupVersion,
		policyapiv1.SchemeGroupVersion,
		rbacv1.SchemeGroupVersion,
		storageapiv1.SchemeGroupVersion,
		schedulingapiv1.SchemeGroupVersion,
	)
	genericServerConfig.MergedResourceConfig = resourceConfig

	return genericServerConfig, nil
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
