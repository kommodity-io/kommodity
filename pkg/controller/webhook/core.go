package webhook

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	expipam_webhook "sigs.k8s.io/cluster-api/exp/ipam/webhooks"
	exp_webhook "sigs.k8s.io/cluster-api/exp/webhooks"
	capi_webhook "sigs.k8s.io/cluster-api/webhooks"
	ctrl "sigs.k8s.io/controller-runtime"
)

func setupCAPI(ctx context.Context, manager ctrl.Manager,
	clusterCache clustercache.ClusterCache) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ClusterClass webhook")

	err := setupClusterClassWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterClass webhook: %w", err)
	}

	logger.Info("Setting up Cluster webhook")

	err = setupClusterWebhookWithManager(manager, clusterCache)
	if err != nil {
		return fmt.Errorf("failed to setup Cluster webhook: %w", err)
	}

	logger.Info("Setting up Machine webhook")

	err = setupMachineWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup Machine webhook: %w", err)
	}

	logger.Info("Setting up MachineSet webhook")

	err = setupMachineSetWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineSet webhook: %w", err)
	}

	logger.Info("Setting up MachineDeployment webhook")

	err = setupMachineDeploymentWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDeployment webhook: %w", err)
	}

	err = setupCAPIExperimental(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup CAPI experimental webhooks: %w", err)
	}

	return setupCAPIAddons(ctx, manager)
}

// setupCAPIAddons registers the webhooks for the CAPI addons resources
// (ClusterResourceSet, ClusterResourceSetBinding, MachineHealthCheck).
func setupCAPIAddons(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up ClusterResourceSet webhook")

	err := setupClusterResourceWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSet webhook: %w", err)
	}

	logger.Info("Setting up ClusterResourceSetBinding webhook")

	err = setupClusterResourceSetBindingWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSetBinding webhook: %w", err)
	}

	logger.Info("Setting up MachineHealthCheck webhook")

	err = setupMachineHealthCheckWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineHealthCheck webhook: %w", err)
	}

	return nil
}

// setupCAPIExperimental registers the webhooks for the experimental CAPI resources
// (MachineDrainRule, MachinePool, IPAddress/IPAddressClaim) whose entries the embedded
// capi-validating/mutating webhook configurations reference.
func setupCAPIExperimental(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up MachineDrainRule webhook")

	err := setupMachineDrainRuleWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDrainRule webhook: %w", err)
	}

	logger.Info("Setting up MachinePool webhook")

	err = setupMachinePoolWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachinePool webhook: %w", err)
	}

	logger.Info("Setting up IPAddress webhook")

	err = setupIPAddressWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup IPAddress webhook: %w", err)
	}

	logger.Info("Setting up IPAddressClaim webhook")

	err = setupIPAddressClaimWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup IPAddressClaim webhook: %w", err)
	}

	return nil
}

func setupClusterClassWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.ClusterClass{
		Client: manager.GetClient(),
	}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterClass webhook: %w", err)
	}

	return nil
}

func setupClusterWebhookWithManager(manager ctrl.Manager, clusterCache clustercache.ClusterCache) error {
	err := (&capi_webhook.Cluster{
		Client:             manager.GetClient(),
		ClusterCacheReader: clusterCache,
	}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup Cluster webhook: %w", err)
	}

	return nil
}

func setupMachineWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.Machine{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup Machine webhook: %w", err)
	}

	return nil
}

func setupMachineSetWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.MachineSet{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineSet webhook: %w", err)
	}

	return nil
}

func setupMachineDeploymentWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.MachineDeployment{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDeployment webhook: %w", err)
	}

	return nil
}

func setupMachineDrainRuleWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.MachineDrainRule{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineDrainRule webhook: %w", err)
	}

	return nil
}

func setupMachinePoolWebhookWithManager(manager ctrl.Manager) error {
	err := (&exp_webhook.MachinePool{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachinePool webhook: %w", err)
	}

	return nil
}

func setupIPAddressWebhookWithManager(manager ctrl.Manager) error {
	// Like upstream CAPI, the IPAddress webhook reads IPAddressClaims through the
	// uncached API reader to avoid pulling every claim into the cache.
	err := (&expipam_webhook.IPAddress{
		Client: manager.GetAPIReader(),
	}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup IPAddress webhook: %w", err)
	}

	return nil
}

func setupIPAddressClaimWebhookWithManager(manager ctrl.Manager) error {
	err := (&expipam_webhook.IPAddressClaim{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup IPAddressClaim webhook: %w", err)
	}

	return nil
}

func setupClusterResourceWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.ClusterResourceSet{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSet webhook: %w", err)
	}

	return nil
}

func setupClusterResourceSetBindingWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.ClusterResourceSetBinding{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterResourceSetBinding webhook: %w", err)
	}

	return nil
}

func setupMachineHealthCheckWebhookWithManager(manager ctrl.Manager) error {
	err := (&capi_webhook.MachineHealthCheck{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup MachineHealthCheck webhook: %w", err)
	}

	return nil
}
