package helpers

import (
	"context"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// CleanupHetznerClusterResourcesWithClient exposes the client-injected cleanup
// for tests that point the hcloud client at a fake API server.
func CleanupHetznerClusterResourcesWithClient(
	ctx context.Context,
	client *hcloud.Client,
	clusterName string,
) {
	cleanupHetznerClusterResources(ctx, client, clusterName)
}
