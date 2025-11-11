package reconciler

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	kubevirt_capi_controller "sigs.k8s.io/cluster-api-provider-kubevirt/controllers"
	"sigs.k8s.io/cluster-api-provider-kubevirt/pkg/infracluster"
	"sigs.k8s.io/cluster-api-provider-kubevirt/pkg/kubevirt"
	"sigs.k8s.io/cluster-api-provider-kubevirt/pkg/workloadcluster"
	ctrl "sigs.k8s.io/controller-runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

type kubevirtModule struct{}

// NewKubevirtModule creates a new module for Kubevirt CAPI.
func NewKubevirtModule() Module {
	return &kubevirtModule{}
}

// Name returns the name of the module.
func (m *kubevirtModule) Name() config.Provider {
	return config.ProviderKubevirt
}

// Setup sets up the Kubevirt CAPI controllers.
func (m *kubevirtModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupKubevirt(ctx, deps.Manager, deps.Options)
}

func setupKubevirt(ctx context.Context, manager ctrl.Manager, options controller.Options) error {
	logger := logging.FromContext(ctx)

	noCachedClient, err := k8sclient.New(manager.GetConfig(), k8sclient.Options{Scheme: manager.GetClient().Scheme()})
	if err != nil {
		return fmt.Errorf("failed to create noCachedClient: %w", err)
	}

	logger.Info("Setting up KubevirtCluster controller")

	err = setupKubevirtClusterWithManager(ctx, manager, noCachedClient)
	if err != nil {
		return fmt.Errorf("failed to setup KubevirtCluster controller: %w", err)
	}

	logger.Info("Setting up KubevirtMachine controller")

	err = setupKubevirtMachineWithManager(ctx, manager, options, noCachedClient)
	if err != nil {
		return fmt.Errorf("failed to setup KubevirtMachine controller: %w", err)
	}

	return nil
}

func setupKubevirtClusterWithManager(ctx context.Context, manager ctrl.Manager, noCachedClient k8sclient.Client) error {
	err := (&kubevirt_capi_controller.KubevirtClusterReconciler{
		Client:       manager.GetClient(),
		APIReader:    manager.GetAPIReader(),
		InfraCluster: infracluster.New(manager.GetClient(), noCachedClient),
		Log:          zapr.NewLogger(logging.FromContext(ctx)),
	}).SetupWithManager(ctx, manager)
	if err != nil {
		return fmt.Errorf("failed to setup kubevirt cluster: %w", err)
	}

	return nil
}

func setupKubevirtMachineWithManager(
	ctx context.Context, 
	manager ctrl.Manager, 
	options controller.Options, 
	noCachedClient k8sclient.Client) error {
	err := (&kubevirt_capi_controller.KubevirtMachineReconciler{
		Client:          manager.GetClient(),
		InfraCluster:    infracluster.New(manager.GetClient(), noCachedClient),
		WorkloadCluster: workloadcluster.New(manager.GetClient()),
		MachineFactory:  kubevirt.DefaultMachineFactory{},
	}).SetupWithManager(ctx, manager, options)
	if err != nil {
		return fmt.Errorf("failed to setup kubevirt machine: %w", err)
	}

	return nil
}
