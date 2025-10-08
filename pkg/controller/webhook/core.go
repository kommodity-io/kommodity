package webhook

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
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

	logger.Info("Setting up ClusterResourceSet webhook")

	err = setupClusterResourceWebhookWithManager(manager)
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
