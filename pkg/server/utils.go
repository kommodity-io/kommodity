package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/logging"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func enhanceScheme(scheme *runtime.Scheme) error {
	err := corev1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add core v1 API to scheme: %w", err)
	}

	err = metav1.AddMetaToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add metav1 API to scheme: %w", err)
	}

	err = apiextensionsv1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add apiextensions v1 API to scheme: %w", err)
	}

	err = apiextensionsinternal.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add apiextensions internal API to scheme: %w", err)
	}

	err = apiregistrationv1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add apiregistration v1 API to scheme: %w", err)
	}

	err = apiregistration.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add apiregistration API to scheme: %w", err)
	}

	err = rbacv1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("failed to add rbac v1 API to scheme: %w", err)
	}

	return nil
}

func setupSecureServingWithSelfSigned(ctx context.Context) (*options.SecureServingOptions, error) {
	secureServing := options.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("0.0.0.0")
	secureServing.BindPort = getAPIServerPort(ctx)

	// Generate self-signed certs for "localhost"
	alternateIPs := []net.IP{
		net.ParseIP("127.0.0.1"), // IPv4
		net.ParseIP("::1"),       // IPv6
	}
	alternateDNS := []string{"localhost", "apiserver-loopback-client"}

	err := secureServing.MaybeDefaultWithSelfSignedCerts("localhost", alternateDNS, alternateIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to generate self-signed certs: %w", err)
	}

	return secureServing, nil
}

func getAPIServerPort(ctx context.Context) int {
	logger := logging.FromContext(ctx)

	apiServerPort := os.Getenv("KOMMODITY_API_SERVER_PORT")
	if apiServerPort == "" {
		logger.Info(fmt.Sprintf("KOMMODITY_API_SERVER_PORT is not set, defaulting to %d", defaultAPIServerPort))

		return defaultAPIServerPort
	}

	apiServerPortInt, err := strconv.Atoi(apiServerPort)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to convert KOMMODITY_API_SERVER_PORT to integer: %v, defaulting to %d",
			apiServerPort, defaultAPIServerPort))

		return defaultAPIServerPort
	}

	return apiServerPortInt
}

func getSupportedGroupKindVersions() []schema.GroupVersion {
	return []schema.GroupVersion{
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
	}
}
