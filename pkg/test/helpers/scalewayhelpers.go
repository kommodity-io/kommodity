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

// WaitForScalewayServer checks for the existence of servers in Scaleway using the provided credentials.
func WaitForScalewayServer(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string, instanceCount int, timeout time.Duration) error {
	// Convert SCW_DEFAULT_REGION to instance.Region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return fmt.Errorf("invalid SCW_DEFAULT_REGION provided: %s", scalewayDefaultRegion)
	}

	zone, err := scw.ParseZone(scalewayDefaultZone)
	if err != nil {
		return fmt.Errorf("invalid SCW_DEFAULT_ZONE provided: %s", scalewayDefaultZone)
	}

	// Check that resources are created in Scaleway
	scwClient, err := scw.NewClient(
		// Get your credentials at https://console.scaleway.com/iam/api-keys
		scw.WithAuth(scalewayAccessKey, scalewaySecretKey),
		// Get more about our availability zones at https://www.scaleway.com/en/docs/console/my-account/reference-content/products-availability/
		scw.WithDefaultRegion(region),
		scw.WithDefaultProjectID(scalewayProjectID),
		scw.WithDefaultZone(zone),
	)
	if err != nil {
		return fmt.Errorf("failed to create Scaleway client: %w", err)
	}

	instanceApi := instance.NewAPI(scwClient)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		response, err := instanceApi.ListServers(&instance.ListServersRequest{})
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
func WaitForScalewayServersDeletion(scalewayAccessKey, scalewaySecretKey, scalewayDefaultRegion, scalewayDefaultZone, scalewayProjectID string, timeout time.Duration) error {
	// Convert SCW_DEFAULT_REGION to scw.Region
	region, err := scw.ParseRegion(scalewayDefaultRegion)
	if err != nil {
		return fmt.Errorf("invalid SCW_DEFAULT_REGION provided: %s", scalewayDefaultRegion)
	}

	zone, err := scw.ParseZone(scalewayDefaultZone)
	if err != nil {
		return fmt.Errorf("invalid SCW_DEFAULT_ZONE provided: %s", scalewayDefaultZone)
	}

	// Wait for resources to be deleted in Scaleway
	scwClient, err := scw.NewClient(
		// Get your credentials at https://console.scaleway.com/iam/api-keys
		scw.WithAuth(scalewayAccessKey, scalewaySecretKey),
		// Get more about our availability zones at https://www.scaleway.com/en/docs/console/my-account/reference-content/products-availability/
		scw.WithDefaultRegion(region),
		scw.WithDefaultProjectID(scalewayProjectID),
		scw.WithDefaultZone(zone),
	)
	if err != nil {
		return fmt.Errorf("failed to create Scaleway client: %w", err)
	}

	instanceApi := instance.NewAPI(scwClient)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		response, err := instanceApi.ListServers(&instance.ListServersRequest{})
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
