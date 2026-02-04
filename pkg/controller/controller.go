// Package controller provides the main controller manager for the Kommodity project.
package controller

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/controller/reconciler"
	"github.com/kommodity-io/kommodity/pkg/controller/webhook"
	"github.com/kommodity-io/kommodity/pkg/logging"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapiserver "k8s.io/apiserver/pkg/server"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	crwebconv "sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

const (
	// MaxConcurrentReconciles is the maximum number of concurrent reconciles for controllers.
	MaxConcurrentReconciles = 10
)

// NewAggregatedControllerManager creates a new controller manager with all relevant providers.
//
//nolint:funlen // Function length is long because of NewManager initialization.
func NewAggregatedControllerManager(ctx context.Context,
	kommodityConfig *config.KommodityConfig,
	genericServerConfig *genericapiserver.RecommendedConfig,
	scheme *runtime.Scheme,
	signingKeyDeps reconciler.SigningKeyDeps) (ctrl.Manager, error) {
	logger := zapr.NewLogger(logging.FromContext(ctx))
	ctrl.SetLogger(logger)

	logger.Info("Creating controller manager")

	webhookServer := getWebhookServerConfig(genericServerConfig, kommodityConfig)
	webhookServer.Register("/convert", crwebconv.NewWebhookHandler(scheme))

	manager, err := ctrl.NewManager(
		genericServerConfig.LoopbackClientConfig,
		ctrl.Options{
			Scheme: scheme,
			Logger: logger,
			Cache: cache.Options{
				Scheme: scheme,
			},
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
			Client: client.Options{
				Scheme: scheme,
				Cache: &client.CacheOptions{
					DisableFor: []client.Object{
						&corev1.ConfigMap{},
						&corev1.Secret{},
						&corev1.Pod{},
						&appsv1.Deployment{},
						&appsv1.DaemonSet{},
					},
				},
			},
			WebhookServer: webhookServer,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager: %w", err)
	}

	controllerOpts := controller.Options{
		MaxConcurrentReconciles: MaxConcurrentReconciles,
		LogConstructor: func(_ *reconcile.Request) logr.Logger {
			return logger
		},
	}

	clusterCache, err := setupClusterCacheWithManager(ctx, manager, controllerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterCache: %w", err)
	}

	logger.Info("Setting up webhooks")

	err = webhook.SetupWebhooks(ctx, &manager, clusterCache)
	if err != nil {
		return nil, fmt.Errorf("failed to setup webhooks: %w", err)
	}

	logger.Info("Setting up reconcilers")

	err = reconciler.SetupReconcilers(ctx, kommodityConfig, &manager, clusterCache, controllerOpts, signingKeyDeps)
	if err != nil {
		return nil, fmt.Errorf("failed to setup reconcilers: %w", err)
	}

	logger.Info("Controller manager created")

	return manager, nil
}

func getWebhookServerConfig(genericServerConfig *genericapiserver.RecommendedConfig,
	kommodityConfig *config.KommodityConfig) ctrlwebhook.Server {
	return ctrlwebhook.NewServer(ctrlwebhook.Options{
		Port:    kommodityConfig.WebhookPort,
		TLSOpts: setupWebhookTLSOptions(genericServerConfig),
	})
}

func setupWebhookTLSOptions(gsc *genericapiserver.RecommendedConfig) []func(*tls.Config) {
	return []func(*tls.Config){
		func(c *tls.Config) {
			certPEM, keyPEM := gsc.SecureServing.Cert.CurrentCertKeyContent()

			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err == nil {
				c.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil }
			}
		},
	}
}
