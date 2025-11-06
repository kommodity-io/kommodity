package reconciler

import (
	"context"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	bootstrap_controller "github.com/siderolabs/cluster-api-bootstrap-provider-talos/controllers"
	control_plane_controller "github.com/siderolabs/cluster-api-control-plane-provider-talos/controllers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

type talosModule struct{}

// NewTalosModule creates a new module for Talos CAPI.
func NewTalosModule() Module {
	return &talosModule{}
}

// Name returns the name of the module.
func (m *talosModule) Name() config.Provider {
	return config.ProviderTalos
}

// Setup sets up the Talos CAPI controllers.
func (m *talosModule) Setup(ctx context.Context, deps SetupDeps) error {
	return setupTalos(ctx, deps.Manager, deps.Options)
}

func setupTalos(ctx context.Context, manager ctrl.Manager,
	opt controller.Options) error {
	logger := logging.FromContext(ctx)

	logger.Info("Setting up TalosConfig controller")

	err := setupTalosConfigWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig controller: %w", err)
	}

	logger.Info("Setting up TalosControlPlane controller")

	err = setupTalosControlPlaneWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	return nil
}

func setupTalosConfigWithManager(ctx context.Context, manager ctrl.Manager,
	opt controller.Options) error {
	err := (&bootstrap_controller.TalosConfigReconciler{
		Client: manager.GetClient(),
		Log:    zapr.NewLogger(logging.FromContext(ctx)),
		Scheme: manager.GetScheme(),
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup TalosConfig controller: %w", err)
	}

	return nil
}

//nolint:staticcheck // Waiting for Talos Reconciler to be updated to controller-runtime v0.11.x
func setupTalosControlPlaneWithManager(ctx context.Context, manager ctrl.Manager, opt controller.Options) error {
	logger := zapr.NewLogger(logging.FromContext(ctx))

	tracker, err := remote.NewClusterCacheTracker(manager,
		remote.ClusterCacheTrackerOptions{
			SecretCachingClient: manager.GetClient(),
			Log:                 &logger,
			ControllerName:      "talos-control-plane-controller",
			ClientUncachedObjects: []client.Object{
				&corev1.ConfigMap{},
				&corev1.Secret{},
				&corev1.Pod{},
				&appsv1.Deployment{},
				&appsv1.DaemonSet{},
			},
		})
	if err != nil {
		return fmt.Errorf("failed to create cluster cache tracker: %w", err)
	}

	err = (&remote.ClusterCacheReconciler{
		Client:  manager.GetClient(),
		Tracker: tracker,
	}).SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup ClusterCacheReconciler: %w", err)
	}

	err = (&control_plane_controller.TalosControlPlaneReconciler{
		Client:    manager.GetClient(),
		APIReader: manager.GetAPIReader(),
		Log:       zapr.NewLogger(logging.FromContext(ctx)),
		Scheme:    manager.GetScheme(),
		Tracker:   tracker,
	}).SetupWithManager(manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	return nil
}
