package helpers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
)

// WaitForScalewayServers checks for the existence of servers in Scaleway using the provided credentials.
func WaitForScalewayServers(clusterName, scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string, instanceCount int, timeout time.Duration) error {
	instanceApi, err := getInstanceAPI(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID)
	if err != nil {
		return fmt.Errorf("failed to get instance API: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestOptions := instance.ListServersRequest{
		Tags: []string{fmt.Sprintf("caps-scalewaycluster=%s", clusterName)},
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		response, err := instanceApi.ListServers(&requestOptions)
		if err != nil {
			return false, fmt.Errorf("failed to list Scaleway servers: %w", err)
		}

		if len(response.Servers) < instanceCount {
			println(fmt.Sprintf("Found %d servers in Scaleway. Waiting for %d", len(response.Servers), instanceCount))
			return false, nil
		}
		if len(response.Servers) == instanceCount {
			println(fmt.Sprintf("Found %d servers in Scaleway", len(response.Servers)))
			return true, nil
		}
		if len(response.Servers) > instanceCount {
			return false, fmt.Errorf("found more servers (%d) than expected (%d) in Scaleway", len(response.Servers), instanceCount)
		}

		return false, fmt.Errorf("This shouldn't happen")
	})

	if err != nil {
		return fmt.Errorf("%d servers not found in Scaleway within timeout: %v", instanceCount, err)
	}

	return nil
}

// WaitForScalewayServersDeletion waits until all servers in Scaleway are deleted using the provided credentials.
func WaitForScalewayServersDeletion(clusterName, scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string, timeout time.Duration) error {
	instanceApi, err := getInstanceAPI(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID)
	if err != nil {
		return fmt.Errorf("failed to get instance API: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestOptions := instance.ListServersRequest{
		Tags: []string{fmt.Sprintf("caps-scalewaycluster=%s", clusterName)},
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		response, err := instanceApi.ListServers(&requestOptions)
		if err != nil {
			return false, fmt.Errorf("failed to list Scaleway servers: %w", err)
		}

		serverCount := len(response.Servers)

		if serverCount == 0 {
			log.Println("All Scaleway servers have been deleted")
			return true, nil
		}

		log.Printf("There are still %d Scaleway servers present", serverCount)

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("Scaleway servers were not deleted within the timeout: %w", err)
	}

	return nil
}

// DeleteAllScalewayServers deletes all servers in a Scaleway Project using the provided credentials.
func DeleteAllScalewayServers(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayProjectID string) error {

	// Get available zones in the region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return fmt.Errorf("invalid region provided: %s", scalewayDefaultRegion)
	}
	zones := region.GetZones()

	for _, zone := range zones {
		instanceApi, err := getInstanceAPI(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, string(zone), scalewayProjectID)
		if err != nil {
			return fmt.Errorf("failed to get instance API for zone %s: %w", zone, err)
		}

		response, err := instanceApi.ListServers(&instance.ListServersRequest{})
		if err != nil {
			return fmt.Errorf("failed to list Scaleway servers in zone %s: %w", zone, err)
		}

		for _, server := range response.Servers {
			if server.State == instance.ServerStateRunning {
				err := instanceApi.ServerActionAndWait(&instance.ServerActionAndWaitRequest{
					ServerID: server.ID,
					Action:   instance.ServerActionPoweroff,
					Zone:     zone,
				})
				if err != nil {
					return fmt.Errorf("failed to power off Scaleway server %s in zone %s: %w", server.ID, zone, err)
				}
			}

			err = instanceApi.DeleteServer(&instance.DeleteServerRequest{
				ServerID: server.ID,
			})
			if err != nil {
				return fmt.Errorf("failed to delete Scaleway server %s in zone %s: %w", server.ID, zone, err)
			}
			log.Printf("Deleted Scaleway server %s in zone %s", server.ID, zone)
		}
	}

	return nil
}

func getInstanceAPI(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string) (*instance.API, error) {
	// Convert SCW_DEFAULT_REGION to instance.Region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return nil, fmt.Errorf("invalid region provided: %s", scalewayDefaultRegion)
	}

	zone, err := scw.ParseZone(scalewayDefaultZone)
	if err != nil {
		return nil, fmt.Errorf("invalid zone provided: %s", scalewayDefaultZone)
	}

	scwClient, err := scw.NewClient(scw.WithAuth(scalewayAccessKey, scalewaySecretKey),
		scw.WithDefaultRegion(region),
		scw.WithDefaultProjectID(scalewayProjectID),
		scw.WithDefaultZone(zone),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Scaleway client: %w", err)
	}

	instanceApi := instance.NewAPI(scwClient)

	return instanceApi, nil
}
