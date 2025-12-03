package server

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/config"
	admissionv1 "k8s.io/api/admission/v1"
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
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

const (
	expectedCertSplitCount = 3
)

func enhanceScheme(scheme *runtime.Scheme) error {
	addFuncs := []struct {
		name string
		fn   func(*runtime.Scheme) error
	}{
		{"admissionv1.AddToScheme", admissionv1.AddToScheme},
		{"admissionregistrationv1.AddToScheme", admissionregistrationv1.AddToScheme},
		{"apiextensionsinternal.AddToScheme", apiextensionsinternal.AddToScheme},
		{"apiextensionsv1.AddToScheme", apiextensionsv1.AddToScheme},
		{"apiregistration.AddToScheme", apiregistration.AddToScheme},
		{"apiregistrationv1.AddToScheme", apiregistrationv1.AddToScheme},
		{"appsv1.AddToScheme", appsv1.AddToScheme},
		{"authenticationv1.AddToScheme", authenticationv1.AddToScheme},
		{"authorizationapiv1.AddToScheme", authorizationapiv1.AddToScheme},
		{"corev1.AddToScheme", corev1.AddToScheme},
		{"discoveryv1.AddToScheme", discoveryv1.AddToScheme},
		{"eventsv1.AddToScheme", eventsv1.AddToScheme},
		{"metav1.AddMetaToScheme", metav1.AddMetaToScheme},
		{"rbacv1.AddToScheme", rbacv1.AddToScheme},
	}

	for _, add := range addFuncs {
		err := add.fn(scheme)
		if err != nil {
			return fmt.Errorf("failed to add %s: %w", add.name, err)
		}
	}

	return nil
}

func mapCoreInternalAliases(scheme *runtime.Scheme) {
	add := func(kind string, groupVersion schema.GroupVersion, obj runtime.Object) {
		if _, exists := scheme.KnownTypes(groupVersion)[kind]; !exists {
			scheme.AddKnownTypeWithName(groupVersion.WithKind(kind), obj)
		}
	}

	gvCoreInternal := schema.GroupVersion{Group: "", Version: runtime.APIVersionInternal}

	add("ConfigMap", gvCoreInternal, &corev1.ConfigMap{})
	add("Secret", gvCoreInternal, &corev1.Secret{})
	add("Event", gvCoreInternal, &corev1.Event{})
	add("Namespace", gvCoreInternal, &corev1.Namespace{})
	add("Service", gvCoreInternal, &corev1.Service{})
	add("Endpoints", gvCoreInternal, &corev1.Endpoints{})
	add("ServiceAccount", gvCoreInternal, &corev1.ServiceAccount{})

	add("ConfigMapList", gvCoreInternal, &corev1.ConfigMapList{})
	add("SecretList", gvCoreInternal, &corev1.SecretList{})
	add("EventList", gvCoreInternal, &corev1.EventList{})
	add("NamespaceList", gvCoreInternal, &corev1.NamespaceList{})
	add("ServiceList", gvCoreInternal, &corev1.ServiceList{})
	add("EndpointsList", gvCoreInternal, &corev1.EndpointsList{})
	add("ServiceAccountList", gvCoreInternal, &corev1.ServiceAccountList{})

	add("ValidatingWebhookConfiguration", gvCoreInternal, &admissionregistrationv1.ValidatingWebhookConfiguration{})
	add("MutatingWebhookConfiguration", gvCoreInternal, &admissionregistrationv1.MutatingWebhookConfiguration{})

	gvRbacInternal := schema.GroupVersion{Group: "rbac.authorization.k8s.io", Version: runtime.APIVersionInternal}

	add("Role", gvRbacInternal, &rbacv1.Role{})
	add("RoleBinding", gvRbacInternal, &rbacv1.RoleBinding{})

	add("RoleList", gvRbacInternal, &rbacv1.RoleList{})
	add("RoleBindingList", gvRbacInternal, &rbacv1.RoleBindingList{})
}

func setupSecureServingWithSelfSigned(cfg *config.KommodityConfig) (*options.SecureServingOptions, error) {
	secureServing := options.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("0.0.0.0")
	secureServing.BindPort = cfg.APIServerPort

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

func getServingCertAndKeyFromFiles(genericServerConfig *genericapiserver.RecommendedConfig) ([]byte, []byte, error) {
	combinedCertName := genericServerConfig.SecureServing.Cert.Name()
	if combinedCertName == "" {
		return nil, nil, ErrWebhookServerCertsNotConfigured
	}

	certNames := strings.Split(combinedCertName, "::")
	if len(certNames) != expectedCertSplitCount {
		return nil, nil, ErrWebhookServerCertKeyNotConfigured
	}

	certDir, certFile := filepath.Split(certNames[1])

	//nolint:gosec // We know that the certFile is a file path.
	crt, err := os.ReadFile(filepath.Join(certDir, certFile))
	if err != nil {
		return nil, nil, fmt.Errorf("read webhook serving cert: %w", err)
	}

	keyDir, keyFile := filepath.Split(certNames[2])

	//nolint:gosec // We know that the certFile is a file path.
	key, err := os.ReadFile(filepath.Join(keyDir, keyFile))
	if err != nil {
		return nil, nil, fmt.Errorf("read webhook serving key: %w", err)
	}

	return crt, key, nil
}

func convertPEMToRSAKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, ErrFailedToDecodePEMBlock
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsedKey, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("%w: %w, %w", ErrFailedToParsePrivateKey, err, err2)
		}

		var success bool

		rsaKey, success = parsedKey.(*rsa.PrivateKey)
		if !success {
			return nil, ErrPrivateKeyNotRSA
		}
	}

	return rsaKey, nil
}

func getSupportedGroupKindVersions() []schema.GroupVersion {
	return []schema.GroupVersion{
		corev1.SchemeGroupVersion,
		apiextensionsv1.SchemeGroupVersion,
		apiregistrationv1.SchemeGroupVersion,
		admissionregistrationv1.SchemeGroupVersion,
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
		schedulingapiv1.SchemeGroupVersion,
		storageapiv1.SchemeGroupVersion,
	}
}

// ContainsAll checks if all elements in 'need' are present in 'have'.
func ContainsAll[T comparable](have, need []T) bool {
	if len(need) == 0 {
		return true
	}

	set := make(map[T]struct{}, len(have))
	for _, x := range have {
		set[x] = struct{}{}
	}

	for _, y := range need {
		if _, ok := set[y]; !ok {
			return false
		}
	}

	return true
}
