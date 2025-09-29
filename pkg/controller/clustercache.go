package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func setupClusterCacheWithManager(ctx context.Context, manager ctrl.Manager,
	maxConcurrentReconciles int) (clustercache.ClusterCache, error) {
	cache, err := clustercache.SetupWithManager(ctx, manager, clustercache.Options{
		SecretClient: manager.GetClient(),
		Client: clustercache.ClientOptions{
			UserAgent: "kommodity-clustercache",
		},
	}, controller.Options{
		MaxConcurrentReconciles: maxConcurrentReconciles,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterCache: %w", err)
	}

	return cache, nil
}
