package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func setupClusterCacheWithManager(ctx context.Context, manager ctrl.Manager,
	opt controller.Options) (clustercache.ClusterCache, error) {
	cache, err := clustercache.SetupWithManager(ctx, manager, clustercache.Options{
		SecretClient: manager.GetClient(),
		Client: clustercache.ClientOptions{
			UserAgent: "kommodity-clustercache",
		},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterCache: %w", err)
	}

	return cache, nil
}
