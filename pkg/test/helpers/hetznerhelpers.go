package helpers

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	hetznerValuesFile = "values.hetzner.yaml"
	hetznerTestSKU    = "cpx22"

	// hetznerTestControlPlanes and hetznerTestWorkers pin the cluster size the
	// integration test creates; HetznerTestServerCount asserts on their sum.
	hetznerTestControlPlanes = 3
	hetznerTestWorkers       = 2

	// HetznerTestServerCount is the number of hcloud servers the integration
	// test expects once the cluster is up.
	HetznerTestServerCount = hetznerTestControlPlanes + hetznerTestWorkers
)

// HetznerInfra holds Hetzner-specific configuration for chart installation.
type HetznerInfra struct{}

// ValuesFile returns the Helm values file for Hetzner.
func (h HetznerInfra) ValuesFile() string { return hetznerValuesFile }

// Overrides returns the Helm value overrides for Hetzner testing. The replica
// counts are pinned here so the test owns the server count it asserts on,
// independent of the example values file.
func (h HetznerInfra) Overrides() map[string]any {
	return map[string]any{
		"kommodity.nodepools.default.sku": hetznerTestSKU,
		"kommodity.controlplane.sku":      hetznerTestSKU,
		// int64: unstructured.SetNestedField rejects plain int values.
		"kommodity.controlplane.replicas":      int64(hetznerTestControlPlanes),
		"kommodity.nodepools.default.replicas": int64(hetznerTestWorkers),
	}
}

// WaitForHetznerServers waits until the expected number of hcloud servers exist for the cluster.
func WaitForHetznerServers(
	ctx context.Context,
	clusterName string,
	expectedCount int,
	timeout time.Duration,
) error {
	client, err := getHetznerClient()
	if err != nil {
		return fmt.Errorf("failed to get Hetzner client: %w", err)
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true,
		func(pollCtx context.Context) (bool, error) {
			servers, err := listHetznerClusterServers(pollCtx, client, clusterName)
			if err != nil {
				return false, err
			}

			switch {
			case len(servers) < expectedCount:
				log.Printf("Found %d servers in Hetzner. Waiting for %d", len(servers), expectedCount)

				return false, nil
			case len(servers) == expectedCount:
				log.Printf("Found %d servers in Hetzner", len(servers))

				return true, nil
			default:
				return false, fmt.Errorf("%w in Hetzner: found %d, expected %d",
					errMoreServersThanExpected, len(servers), expectedCount)
			}
		})
	if err != nil {
		return fmt.Errorf("%d servers not found in Hetzner within timeout: %w", expectedCount, err)
	}

	return nil
}

// WaitForHetznerServersDeletion waits until all hcloud servers of the cluster are deleted.
func WaitForHetznerServersDeletion(
	ctx context.Context,
	clusterName string,
	timeout time.Duration,
) error {
	client, err := getHetznerClient()
	if err != nil {
		return fmt.Errorf("failed to get Hetzner client: %w", err)
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true,
		func(pollCtx context.Context) (bool, error) {
			servers, err := listHetznerClusterServers(pollCtx, client, clusterName)
			if err != nil {
				return false, err
			}

			if len(servers) == 0 {
				log.Printf("All Hetzner servers have been deleted")

				return true, nil
			}

			log.Printf("There are still %d Hetzner servers present", len(servers))

			return false, nil
		})
	if err != nil {
		return fmt.Errorf("hetzner servers were not deleted within the timeout: %w", err)
	}

	return nil
}

// CleanupHetznerClusterResources deletes any hcloud resources still labeled as
// owned by the given CAPH cluster. Best effort: errors are logged, never
// returned, so it is safe to wire as t.Cleanup even when the test failed
// mid-teardown and finalizers never ran.
func CleanupHetznerClusterResources(ctx context.Context, clusterName string) {
	client, err := getHetznerClient()
	if err != nil {
		log.Printf("Hetzner cleanup: failed to get client: %v", err)

		return
	}

	cleanupHetznerClusterResources(ctx, client, clusterName)
}

// cleanupHetznerClusterResources deletes all hcloud resources labeled as owned
// by the given CAPH cluster. Best effort: errors are logged, never returned.
// Servers go first and networks last, since a network cannot be deleted while
// servers are still attached; server deletion is async, so a network delete
// racing a slow server teardown may still fail - logged, not fatal.
func cleanupHetznerClusterResources(ctx context.Context, client *hcloud.Client, clusterName string) {
	opts := hcloud.ListOpts{LabelSelector: hetznerClusterLabelSelector(clusterName)}

	servers, err := client.Server.AllWithOpts(ctx, hcloud.ServerListOpts{ListOpts: opts})
	if err != nil {
		log.Printf("Hetzner cleanup: failed to list servers: %v", err)
	}

	for _, server := range servers {
		_, _, err := client.Server.DeleteWithResult(ctx, server)
		logHetznerCleanup("server", server.Name, err)
	}

	loadBalancers, err := client.LoadBalancer.AllWithOpts(ctx, hcloud.LoadBalancerListOpts{ListOpts: opts})
	if err != nil {
		log.Printf("Hetzner cleanup: failed to list load balancers: %v", err)
	}

	for _, loadBalancer := range loadBalancers {
		_, err := client.LoadBalancer.Delete(ctx, loadBalancer)
		logHetznerCleanup("load balancer", loadBalancer.Name, err)
	}

	placementGroups, err := client.PlacementGroup.AllWithOpts(ctx, hcloud.PlacementGroupListOpts{ListOpts: opts})
	if err != nil {
		log.Printf("Hetzner cleanup: failed to list placement groups: %v", err)
	}

	for _, placementGroup := range placementGroups {
		_, err := client.PlacementGroup.Delete(ctx, placementGroup)
		logHetznerCleanup("placement group", placementGroup.Name, err)
	}

	networks, err := client.Network.AllWithOpts(ctx, hcloud.NetworkListOpts{ListOpts: opts})
	if err != nil {
		log.Printf("Hetzner cleanup: failed to list networks: %v", err)
	}

	for _, network := range networks {
		_, err := client.Network.Delete(ctx, network)
		logHetznerCleanup("network", network.Name, err)
	}
}

// logHetznerCleanup logs the outcome of one best-effort deletion.
func logHetznerCleanup(kind string, name string, err error) {
	if err != nil {
		log.Printf("Hetzner cleanup: failed to delete %s %s: %v", kind, name, err)

		return
	}

	log.Printf("Hetzner cleanup: deleted leaked %s %s", kind, name)
}

// hetznerClusterLabelSelector returns the label selector CAPH puts on every
// hcloud resource it owns for the given cluster.
func hetznerClusterLabelSelector(clusterName string) string {
	return "caph-cluster-" + clusterName + "=owned"
}

// listHetznerClusterServers lists the hcloud servers owned by the given CAPH cluster.
func listHetznerClusterServers(
	ctx context.Context,
	client *hcloud.Client,
	clusterName string,
) ([]*hcloud.Server, error) {
	servers, err := client.Server.AllWithOpts(ctx, hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{
			LabelSelector: hetznerClusterLabelSelector(clusterName),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list Hetzner servers: %w", err)
	}

	return servers, nil
}

func getHetznerClient() (*hcloud.Client, error) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		return nil, errHcloudTokenNotSet
	}

	return hcloud.NewClient(hcloud.WithToken(token)), nil
}
