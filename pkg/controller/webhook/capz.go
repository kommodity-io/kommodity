package webhook

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	capzv1beta1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capzexpv1beta1 "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func setupCAPZ(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up AzureCluster webhook")

	err := setupAzureClusterWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureCluster webhook: %w", err)
	}

	logger.Info("Setting up AzureClusterTemplate webhook")

	err = setupAzureClusterTemplateWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureClusterTemplate webhook: %w", err)
	}

	logger.Info("Setting up AzureMachine webhook")

	err = setupAzureMachineWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachine webhook: %w", err)
	}

	logger.Info("Setting up AzureMachineTemplate webhook")

	err = setupAzureMachineTemplateWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachineTemplate webhook: %w", err)
	}

	logger.Info("Setting up AzureMachinePool webhook")

	err = setupAzureMachinePoolWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachinePool webhook: %w", err)
	}

	return nil
}

func setupAzureClusterWebhookWithManager(manager ctrl.Manager) error {
	err := (&capzv1beta1.AzureCluster{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureCluster webhook with manager: %w", err)
	}

	return nil
}

func setupAzureClusterTemplateWebhookWithManager(manager ctrl.Manager) error {
	err := (&capzv1beta1.AzureClusterTemplate{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureClusterTemplate webhook with manager: %w", err)
	}

	return nil
}

func setupAzureMachineWebhookWithManager(manager ctrl.Manager) error {
	err := capzv1beta1.SetupAzureMachineWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachine webhook with manager: %w", err)
	}

	return nil
}

func setupAzureMachineTemplateWebhookWithManager(manager ctrl.Manager) error {
	err := (&capzv1beta1.AzureMachineTemplate{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachineTemplate webhook with manager: %w", err)
	}

	return nil
}

func setupAzureMachinePoolWebhookWithManager(manager ctrl.Manager) error {
	err := capzexpv1beta1.SetupAzureMachinePoolWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup AzureMachinePool webhook with manager: %w", err)
	}

	return nil
}
