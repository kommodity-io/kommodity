package reconciler

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	bootstrap_controller "github.com/siderolabs/cluster-api-bootstrap-provider-talos/controllers"
	controlplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	control_plane_controller "github.com/siderolabs/cluster-api-control-plane-provider-talos/controllers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type talosModule struct{}

// NewTalosModule creates a new module for Talos CAPI.
func NewTalosModule() Module {
	return &talosModule{}
}

// talosControlPlaneWrapper wraps the Siderolabs TalosControlPlaneReconciler.
// For private network clusters, it runs the Siderolabs reconciler for machine management
// and then runs the PrivateNetworkControlPlaneReconciler to set correct health conditions.
type talosControlPlaneWrapper struct {
	*control_plane_controller.TalosControlPlaneReconciler
	WrapperClient            client.Client
	PrivateNetworkReconciler *PrivateNetworkControlPlaneReconciler
}

// Reconcile delegates to the Siderolabs reconciler. For private network clusters,
// it also runs the PrivateNetworkControlPlaneReconciler afterward to ensure correct
// health conditions are set (the Siderolabs reconciler sets them incorrectly because
// the Talos API is unreachable).
func (w *talosControlPlaneWrapper) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	tcp := &controlplanev1.TalosControlPlane{}

	err := w.WrapperClient.Get(ctx, req.NamespacedName, tcp)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get TalosControlPlane: %w", err)
	}

	if !isPrivateNetworkCluster(tcp) {
		result, err := w.TalosControlPlaneReconciler.Reconcile(ctx, req)
		if err != nil {
			return result, fmt.Errorf("siderolabs reconciler failed: %w", err)
		}

		return result, nil
	}

	return w.reconcilePrivateNetwork(ctx, req)
}

// reconcilePrivateNetwork runs the Siderolabs reconciler for machine management,
// then runs the PrivateNetworkControlPlaneReconciler to overwrite the incorrect
// health conditions with correct ones based on Kubernetes API checks.
func (w *talosControlPlaneWrapper) reconcilePrivateNetwork(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Run the Siderolabs reconciler for machine management and scaling.
	// Health check errors are expected since Talos API is unreachable.
	result, _ := w.TalosControlPlaneReconciler.Reconcile(ctx, req)

	// Run the PrivateNetworkControlPlaneReconciler to set correct health conditions.
	// This overwrites the incorrect conditions the Siderolabs reconciler just set.
	pnResult, pnErr := w.PrivateNetworkReconciler.Reconcile(ctx, req)
	if pnErr != nil {
		return result, fmt.Errorf("private network reconciler failed: %w", pnErr)
	}

	// Use the shorter requeue interval from either reconciler.
	if pnResult.RequeueAfter > 0 && (result.RequeueAfter == 0 || pnResult.RequeueAfter < result.RequeueAfter) {
		result.RequeueAfter = pnResult.RequeueAfter
	}

	return result, nil
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

	// Create the PrivateNetworkControlPlaneReconciler first so we can pass it to the wrapper.
	privateNetworkReconciler := &PrivateNetworkControlPlaneReconciler{
		Client:  manager.GetClient(),
		Log:     logger,
		Scheme:  manager.GetScheme(),
		Tracker: tracker,
	}

	err = setupWrappedTalosControlPlane(manager, logger, tracker, opt, privateNetworkReconciler)
	if err != nil {
		return fmt.Errorf("failed to setup TalosControlPlane controller: %w", err)
	}

	// Setup the PrivateNetworkControlPlaneReconciler as its own controller
	// so it also runs independently on its own requeue schedule.
	logger.Info("Setting up PrivateNetworkControlPlane controller")

	err = privateNetworkReconciler.SetupWithManager(ctx, manager, opt)
	if err != nil {
		return fmt.Errorf("failed to setup PrivateNetworkControlPlane controller: %w", err)
	}

	return nil
}

// setupWrappedTalosControlPlane registers the Siderolabs TalosControlPlane reconciler
// wrapped to run the PrivateNetworkControlPlaneReconciler after each Siderolabs reconcile
// for private network clusters.
//
//nolint:staticcheck // Waiting for Talos Reconciler to be updated to controller-runtime v0.11.x
func setupWrappedTalosControlPlane(
	manager ctrl.Manager,
	logger logr.Logger,
	tracker *remote.ClusterCacheTracker,
	opt controller.Options,
	pnReconciler *PrivateNetworkControlPlaneReconciler,
) error {
	siderolabsReconciler := &control_plane_controller.TalosControlPlaneReconciler{
		Client:    manager.GetClient(),
		APIReader: manager.GetAPIReader(),
		Log:       logger,
		Scheme:    manager.GetScheme(),
		Tracker:   tracker,
	}

	wrapper := &talosControlPlaneWrapper{
		TalosControlPlaneReconciler: siderolabsReconciler,
		WrapperClient:               manager.GetClient(),
		PrivateNetworkReconciler:    pnReconciler,
	}

	err := ctrl.NewControllerManagedBy(manager).
		For(&controlplanev1.TalosControlPlane{}).
		Owns(&clusterv1.Machine{}).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(siderolabsReconciler.ClusterToTalosControlPlane),
		).
		WithOptions(opt).
		Complete(wrapper)
	if err != nil {
		return fmt.Errorf("failed to setup wrapped TalosControlPlane controller: %w", err)
	}

	return nil
}
