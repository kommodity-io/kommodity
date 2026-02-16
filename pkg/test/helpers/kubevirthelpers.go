package helpers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	virtualMachineGroup    = "kubevirt.io"
	virtualMachineVersion  = "v1"
	virtualMachineResource = "virtualmachines"
)

func newVirtualMachineGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    virtualMachineGroup,
		Version:  virtualMachineVersion,
		Resource: virtualMachineResource,
	}
}

// countMatchingVMs counts VirtualMachine resources whose name contains the cluster name.
func countMatchingVMs(
	ctx context.Context,
	dynClient dynamic.Interface,
	namespace string,
	clusterName string,
) (int, error) {
	list, err := dynClient.Resource(newVirtualMachineGVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list VirtualMachines: %w", err)
	}

	matchCount := 0

	for _, item := range list.Items {
		if strings.Contains(item.GetName(), clusterName) {
			matchCount++
		}
	}

	return matchCount, nil
}

// WaitForKubevirtVMs waits until the expected number of VirtualMachine resources
// matching the cluster name are found in the given namespace.
func WaitForKubevirtVMs(
	config *rest.Config,
	namespace string,
	clusterName string,
	expectedCount int,
	timeout time.Duration,
) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(_ context.Context) (bool, error) {
		matchCount, countErr := countMatchingVMs(ctx, dynClient, namespace, clusterName)
		if countErr != nil {
			return false, countErr
		}

		if matchCount >= expectedCount {
			log.Printf("Found %d VirtualMachines matching %q in namespace %s (expected at least %d)",
				matchCount, clusterName, namespace, expectedCount)

			return true, nil
		}

		log.Printf("Found %d VirtualMachines matching %q in namespace %s, waiting for at least %d",
			matchCount, clusterName, namespace, expectedCount)

		return false, nil
	})
	if err != nil {
		return fmt.Errorf(
			"%d VirtualMachines matching %q not found in namespace %s within timeout: %w",
			expectedCount, clusterName, namespace, err,
		)
	}

	return nil
}

// WaitForKubevirtVMsDeletion waits until all VirtualMachine resources matching
// the cluster name are deleted from the given namespace.
func WaitForKubevirtVMsDeletion(
	config *rest.Config,
	namespace string,
	clusterName string,
	timeout time.Duration,
) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(_ context.Context) (bool, error) {
		matchCount, countErr := countMatchingVMs(ctx, dynClient, namespace, clusterName)
		if countErr != nil {
			return false, countErr
		}

		if matchCount == 0 {
			log.Printf("All VirtualMachines matching %q have been deleted from namespace %s",
				clusterName, namespace)

			return true, nil
		}

		log.Printf("There are still %d VirtualMachines matching %q in namespace %s",
			matchCount, clusterName, namespace)

		return false, nil
	})
	if err != nil {
		return fmt.Errorf(
			"VirtualMachines matching %q were not deleted from namespace %s within timeout: %w",
			clusterName, namespace, err,
		)
	}

	return nil
}
