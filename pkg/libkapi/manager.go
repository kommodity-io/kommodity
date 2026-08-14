package libkapi

import (
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// disabledBindAddress turns off a controller-runtime HTTP listener (metrics,
// health probes) that libkapi doesn't need — it serves its own /healthz.
const disabledBindAddress = "0"

// webhookHost binds the webhook server to loopback only: it exists to
// answer admission/conversion calls from the API server built into this
// same process, never a Service or any other host, so there's no reason to
// expose it beyond localhost — see WebhookConfig's doc.
const webhookHost = "127.0.0.1"

// buildManager builds the single ctrl.Manager libkapi owns for the life of
// the Server, using loopbackConfig (the server's own privileged loopback
// identity) and scheme (the exact same *runtime.Scheme value the REST layer
// uses, so WithScheme-registered types are visible to the manager's
// client). Returns (nil, nil) if no Controller was registered — nothing is
// built or started in that case.
//
// Every registered Controller's SetupWithManager is called here,
// synchronously, in registration order; an error fails New.
func buildManager(
	cfg config,
	loopbackConfig *restclient.Config,
	scheme *runtime.Scheme,
	logger *slog.Logger,
) (ctrl.Manager, error) {
	if len(cfg.controllers) == 0 {
		//nolint:nilnil // no Controller registered is not an error - nil Manager is the documented, valid result.
		return nil, nil
	}

	options := ctrl.Options{
		Scheme:                 scheme,
		Logger:                 logr.FromSlogHandler(logger.Handler()),
		Metrics:                metricsserver.Options{BindAddress: disabledBindAddress},
		HealthProbeBindAddress: disabledBindAddress,
	}

	if cfg.leaderElection != nil {
		namespace := cfg.leaderElection.Namespace
		if namespace == "" {
			namespace = defaultLeaderElectionNamespace
		}

		options.LeaderElection = true
		options.LeaderElectionResourceLock = resourcelock.LeasesResourceLock
		options.LeaderElectionID = cfg.leaderElection.ID
		options.LeaderElectionNamespace = namespace
		options.LeaderElectionConfig = loopbackConfig
	}

	if cfg.webhook != nil {
		err := ensureSelfSignedWebhookCert(cfg.webhook.DNSNames)
		if err != nil {
			return nil, fmt.Errorf("failed to provision webhook serving certificate: %w", err)
		}

		options.WebhookServer = webhook.NewServer(webhook.Options{Host: webhookHost, Port: cfg.webhook.Port})
	}

	mgr, err := ctrl.NewManager(loopbackConfig, options)
	if err != nil {
		return nil, fmt.Errorf("failed to build controller manager: %w", err)
	}

	for controllerIndex, controller := range cfg.controllers {
		// Wire in the fully-resolved webhook config (nil if WithWebhookServer
		// was never used) before SetupWithManager runs - see webhookAware's
		// doc for why this can't be done inside an Option closure instead.
		if aware, ok := controller.(webhookAware); ok {
			aware.setWebhookConfig(cfg.webhook)
		}

		err := controller.SetupWithManager(mgr)
		if err != nil {
			return nil, fmt.Errorf("failed to set up controller %d: %w", controllerIndex, err)
		}
	}

	return mgr, nil
}
