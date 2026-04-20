package api

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	clientgoclientset "k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterDetail holds detailed information about a cluster.
type ClusterDetail struct {
	ClusterInfo

	MachineDeployments []MachineDeploymentDetail
}

// MachineDeploymentDetail holds information about a MachineDeployment.
type MachineDeploymentDetail struct {
	Name     string
	Phase    string
	Replicas *int32
	MinSize  *int32
	MaxSize  *int32
	Machines []MachineDetail
}

// MachineDetail holds information about a Machine.
type MachineDetail struct {
	Name              string
	NodeName          string
	Phase             string
	KubernetesVersion string
}

// GetClusterDetail retrieves detailed information about a specific cluster.
func GetClusterDetail(
	ctx context.Context,
	client ctrlclient.Client,
	kubeClient *clientgoclientset.Clientset,
	clusterName string,
	logger *zap.Logger,
) (*ClusterDetail, error) {
	// Get cluster list to find the target cluster
	clusters, err := GetClusterList(ctx, client, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster list: %w", err)
	}

	// Find the target cluster
	var clusterInfo *ClusterInfo

	for i := range clusters {
		if clusters[i].Name == clusterName {
			clusterInfo = &clusters[i]

			break
		}
	}

	if clusterInfo == nil {
		return nil, fmt.Errorf("%w: %s", ErrClusterNotFound, clusterName)
	}

	// Get MachineDeployments
	machineDeployments, err := getMachineDeployments(ctx, client, clusterName, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine deployments: %w", err)
	}

	return &ClusterDetail{
		ClusterInfo:        *clusterInfo,
		MachineDeployments: machineDeployments,
	}, nil
}

// getMachineDeployments retrieves all MachineDeployments for a cluster.
func getMachineDeployments(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	logger *zap.Logger,
) ([]MachineDeploymentDetail, error) {
	mdList := &clusterv1.MachineDeploymentList{}

	err := client.List(ctx, mdList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list machine deployments: %w", err)
	}

	// Fetch all machines once to avoid N+1 queries
	machinesByDeployment, err := getMachinesGroupedByDeployment(ctx, client)
	if err != nil {
		logger.Warn("Failed to get machines", zap.Error(err))
		// Continue with empty machines rather than failing
		machinesByDeployment = make(map[string][]MachineDetail)
	}

	var result []MachineDeploymentDetail

	for i := range mdList.Items {
		deployment := &mdList.Items[i]

		// Get autoscaler annotations
		minSize := getAutoscalerAnnotation(
			deployment.Annotations,
			"cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size",
		)
		maxSize := getAutoscalerAnnotation(
			deployment.Annotations,
			"cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size",
		)

		// Get machines for this deployment from the grouped map
		machines := machinesByDeployment[deployment.Name]

		phase := deployment.Status.Phase
		if phase == "" {
			phase = UnknownVersion
		}

		detail := MachineDeploymentDetail{
			Name:     deployment.Name,
			Phase:    phase,
			Replicas: deployment.Spec.Replicas,
			MinSize:  minSize,
			MaxSize:  maxSize,
			Machines: machines,
		}

		result = append(result, detail)
	}

	return result, nil
}

// getMachinesGroupedByDeployment fetches all machines once and groups them by deployment.
// This avoids N+1 queries when fetching machines for multiple deployments.
func getMachinesGroupedByDeployment(
	ctx context.Context,
	client ctrlclient.Client,
) (map[string][]MachineDetail, error) {
	machineList := &clusterv1.MachineList{}

	err := client.List(ctx, machineList, ctrlclient.InNamespace(DefaultNamespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	// Group machines by their owning MachineDeployment
	result := make(map[string][]MachineDetail)

	for i := range machineList.Items {
		machine := &machineList.Items[i]

		// Find which deployment this machine belongs to
		deploymentName := getDeploymentNameFromMachine(machine)
		if deploymentName == "" {
			continue
		}

		nodeName := UnknownVersion
		if machine.Status.NodeRef != nil {
			nodeName = machine.Status.NodeRef.Name
		}

		kubernetesVersion := UnknownVersion
		if machine.Spec.Version != nil {
			kubernetesVersion = *machine.Spec.Version
		}

		phase := machine.Status.Phase
		if phase == "" {
			phase = UnknownVersion
		}

		detail := MachineDetail{
			Name:              machine.Name,
			NodeName:          nodeName,
			Phase:             phase,
			KubernetesVersion: kubernetesVersion,
		}

		result[deploymentName] = append(result[deploymentName], detail)
	}

	return result, nil
}

// getDeploymentNameFromMachine extracts the deployment name from a machine's owner references.
// Returns empty string if the machine doesn't belong to a deployment.
func getDeploymentNameFromMachine(machine *clusterv1.Machine) string {
	for _, owner := range machine.OwnerReferences {
		if owner.Kind == "MachineSet" {
			// MachineSet name format: <deployment-name>-<hash>
			// Extract deployment name by removing the hash suffix
			name := owner.Name

			lastDash := strings.LastIndex(name, "-")
			if lastDash > 0 {
				return name[:lastDash]
			}
		}
	}

	return ""
}

// getAutoscalerAnnotation retrieves and parses an autoscaler annotation.
func getAutoscalerAnnotation(annotations map[string]string, key string) *int32 {
	if annotations == nil {
		return nil
	}

	value, ok := annotations[key]
	if !ok {
		return nil
	}

	var result int32

	_, err := fmt.Sscanf(value, "%d", &result)
	if err != nil {
		return nil
	}

	return &result
}
