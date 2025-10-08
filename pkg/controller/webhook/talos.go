package webhook

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kommodity-io/kommodity/pkg/logging"
	bootstrapv1alpha3 "github.com/siderolabs/cluster-api-bootstrap-provider-talos/api/v1alpha3"
	controlplanev1alpha3 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
)

func setupTalos(ctx context.Context, manager ctrl.Manager) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up TalosConfig webhook")

	err := setupTalosConfigWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig webhook: %w", err)
	}

	logger.Info("Setting up TalosConfigTemplate webhook")

	err = setupTalosConfigTemplateWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfigTemplate webhook: %w", err)
	}

	logger.Info("Setting up TalosControlPlane webhook")

	err = setupTalosControlPlaneWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane webhook: %w", err)
	}

	return nil
}

func setupTalosConfigWebhookWithManager(manager ctrl.Manager) error {
	err := (&bootstrapv1alpha3.TalosConfig{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig webhook with manager: %w", err)
	}

	return nil
}

func setupTalosConfigTemplateWebhookWithManager(manager ctrl.Manager) error {
	err := (&bootstrapv1alpha3.TalosConfigTemplate{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfigTemplate webhook with manager: %w", err)
	}

	return nil
}

func setupTalosControlPlaneWebhookWithManager(manager ctrl.Manager) error {
	err := (&controlplanev1alpha3.TalosControlPlane{}).SetupWebhookWithManager(manager)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane webhook with manager: %w", err)
	}

	return nil
}
