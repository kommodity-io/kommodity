// Package azurearm provides an in-process reconciler that materializes Azure
// Service Operator (ASO) custom resources directly into Azure via the Azure
// Resource Manager (ARM) API. It is a drop-in, single-binary replacement for the
// external ASO sidecar: it reuses ASO's public generated CR types
// (ConvertToARM/PopulateFromARM) and satisfies the readiness contract that the
// Cluster API Provider Azure (CAPZ) controllers depend on.
package azurearm

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// SetupReconcilers registers one generic ARM reconciler per managed Azure
// resource kind against the provided manager.
func SetupReconcilers(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
	azureCfg *config.AzureConfig,
) error {
	logger := logging.FromContext(ctx)

	resources := managedResources()

	logger.Info("Setting up embedded Azure ARM reconciler",
		zap.Int("managedResourceKinds", len(resources)))

	creds := newCredentialProvider(mgr.GetClient(), azureCfg.DefaultCredentialSecret)

	for _, resource := range resources {
		reconciler := &Reconciler{
			Client:         mgr.GetClient(),
			controllerName: resource.controllerName,
			newObj:         resource.newObj,
			armIDFor:       resource.armIDFor,
			creds:          creds,
		}

		err := reconciler.SetupWithManager(ctx, mgr, opt)
		if err != nil {
			return fmt.Errorf("setting up %s reconciler: %w", resource.controllerName, err)
		}
	}

	return nil
}
