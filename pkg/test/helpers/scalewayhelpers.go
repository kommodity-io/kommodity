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

const (
	scalewayValuesFile = "values.scaleway.yaml"
)

// ScalewayInfra holds Scaleway-specific configuration for chart installation.
type ScalewayInfra struct {
	ProjectID string
}

// WaitForScalewayServers checks for the existence of servers in Scaleway using the provided credentials.
func WaitForScalewayServers(
	clusterName, scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion string,
	scalewayDefaultZone, scalewayProjectID string,
	instanceCount int,
	timeout time.Duration,
) error {
	instanceAPI, err := getInstanceAPI(
		scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID,
	)
	if err != nil {
		return fmt.Errorf("failed to get instance API: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestOptions := instance.ListServersRequest{
		Tags: []string{"caps-scalewaycluster=" + clusterName},
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(_ context.Context) (bool, error) {
		response, err := instanceAPI.ListServers(&requestOptions)
		if err != nil {
			return false, fmt.Errorf("failed to list Scaleway servers: %w", err)
		}

		if len(response.Servers) < instanceCount {
			log.Printf("Found %d servers in Scaleway. Waiting for %d", len(response.Servers), instanceCount)

			return false, nil
		}

		if len(response.Servers) == instanceCount {
			log.Printf("Found %d servers in Scaleway", len(response.Servers))

			return true, nil
		}

		if len(response.Servers) > instanceCount {
			return false, fmt.Errorf("%w: found %d, expected %d",
				errMoreServersThanExpected, len(response.Servers), instanceCount)
		}

		return false, errUnexpectedState
	})
	if err != nil {
		return fmt.Errorf("%d servers not found in Scaleway within timeout: %w", instanceCount, err)
	}

	return nil
}

// WaitForScalewayServersDeletion waits until all servers in Scaleway are deleted using the provided credentials.
func WaitForScalewayServersDeletion(
	clusterName, scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion string,
	scalewayDefaultZone, scalewayProjectID string,
	timeout time.Duration,
) error {
	instanceAPI, err := getInstanceAPI(
		scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID,
	)
	if err != nil {
		return fmt.Errorf("failed to get instance API: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestOptions := instance.ListServersRequest{
		Tags: []string{"caps-scalewaycluster=" + clusterName},
	}

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(_ context.Context) (bool, error) {
		response, err := instanceAPI.ListServers(&requestOptions)
		if err != nil {
			return false, fmt.Errorf("failed to list Scaleway servers: %w", err)
		}

		serverCount := len(response.Servers)

		if serverCount == 0 {
			log.Printf("All Scaleway servers have been deleted")

			return true, nil
		}

		log.Printf("There are still %d Scaleway servers present", serverCount)

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("scaleway servers were not deleted within the timeout: %w", err)
	}

	return nil
}

// DeleteAllScalewayServers deletes all servers in a Scaleway Project using the provided credentials.
func DeleteAllScalewayServers(
	scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayProjectID string,
) error {
	// Get available zones in the region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return fmt.Errorf("%w: %s", errInvalidRegion, scalewayDefaultRegion)
	}

	zones := region.GetZones()

	for _, zone := range zones {
		instanceAPI, err := getInstanceAPI(
			scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, string(zone), scalewayProjectID,
		)
		if err != nil {
			return fmt.Errorf("failed to get instance API for zone %s: %w", zone, err)
		}

		response, err := instanceAPI.ListServers(&instance.ListServersRequest{})
		if err != nil {
			return fmt.Errorf("failed to list Scaleway servers in zone %s: %w", zone, err)
		}

		for _, server := range response.Servers {
			if server.State == instance.ServerStateRunning {
				err := instanceAPI.ServerActionAndWait(&instance.ServerActionAndWaitRequest{
					ServerID: server.ID,
					Action:   instance.ServerActionPoweroff,
					Zone:     zone,
				})
				if err != nil {
					return fmt.Errorf("failed to power off Scaleway server %s in zone %s: %w", server.ID, zone, err)
				}
			}

			err = instanceAPI.DeleteServer(&instance.DeleteServerRequest{
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

func getInstanceAPI(
	scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string,
) (*instance.API, error) {
	// Convert SCW_DEFAULT_REGION to instance.Region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errInvalidRegion, scalewayDefaultRegion)
	}

	zone, err := scw.ParseZone(scalewayDefaultZone)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errInvalidZone, scalewayDefaultZone)
	}

	scwClient, err := scw.NewClient(scw.WithAuth(scalewayAccessKey, scalewaySecretKey),
		scw.WithDefaultRegion(region),
		scw.WithDefaultProjectID(scalewayProjectID),
		scw.WithDefaultZone(zone),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Scaleway client: %w", err)
	}

	instanceAPI := instance.NewAPI(scwClient)

	return instanceAPI, nil
}
