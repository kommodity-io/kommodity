package server

import (
	"fmt"

	"k8s.io/apiserver/pkg/admission/plugin/policy/mutating"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	webhookinit "k8s.io/apiserver/pkg/admission/plugin/webhook/initializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	webhookutil "k8s.io/apiserver/pkg/util/webhook"
	restclientdynamic "k8s.io/client-go/dynamic"
	clientgoclientset "k8s.io/client-go/kubernetes"
)

func applyAdmission(genericServerConfig *genericapiserver.RecommendedConfig,
	kubeClient *clientgoclientset.Clientset) error {
	dynamicClient, err := restclientdynamic.NewForConfig(
		genericServerConfig.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	webhookInitializer := webhookinit.NewPluginInitializer(
		webhookutil.NewDefaultAuthenticationInfoResolverWrapper(
			nil, nil, genericServerConfig.LoopbackClientConfig, nil),
		webhookutil.NewDefaultServiceResolver())

	admissionOpts := options.NewAdmissionOptions()
	admissionOpts.EnablePlugins = []string{"NamespaceLifecycle", "MutatingAdmissionWebhook", "ValidatingAdmissionWebhook"}
	admissionOpts.DisablePlugins = []string{validating.PluginName, mutating.PluginName}

	err = admissionOpts.ApplyTo(&genericServerConfig.Config, genericServerConfig.SharedInformerFactory,
		kubeClient, dynamicClient, genericServerConfig.FeatureGate, webhookInitializer)
	if err != nil {
		return fmt.Errorf("apply admission (main server): %w", err)
	}

	return nil
}
