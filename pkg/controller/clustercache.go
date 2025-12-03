package controller

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/controller/index"
	kubeindex "sigs.k8s.io/cluster-api/api/v1beta1/index"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func setupClusterCacheWithManager(ctx context.Context, manager ctrl.Manager,
	opt controller.Options) (clustercache.ClusterCache, error) {
	err := kubeindex.AddDefaultIndexes(ctx, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to add default indexes: %w", err)
	}

	cache, err := clustercache.SetupWithManager(ctx, manager, clustercache.Options{
		SecretClient: manager.GetClient(),
		Cache: clustercache.CacheOptions{
			Indexes: []clustercache.CacheOptionsIndex{
				clustercache.NodeProviderIDIndex,
				index.NodeNameIndex,
			},
		},
		Client: clustercache.ClientOptions{
			UserAgent: "kommodity-clustercache",
		},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ClusterCache: %w", err)
	}

	return cache, nil
}
