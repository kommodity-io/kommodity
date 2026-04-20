package api

import (
	"context"
	"fmt"

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

		// Get machines
		machines, err := getMachinesForDeployment(ctx, client, deployment.Name, logger)
		if err != nil {
			logger.Warn("Failed to get machines for deployment",
				zap.String("deployment", deployment.Name),
				zap.Error(err),
			)
		}

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

// getMachinesForDeployment retrieves all Machines for a MachineDeployment.
func getMachinesForDeployment(
	ctx context.Context,
	client ctrlclient.Client,
	deploymentName string,
	_ *zap.Logger,
) ([]MachineDetail, error) {
	machineList := &clusterv1.MachineList{}

	err := client.List(ctx, machineList, ctrlclient.InNamespace(DefaultNamespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	var result []MachineDetail

	for i := range machineList.Items {
		machine := &machineList.Items[i]

		// Check if machine belongs to this deployment
		if !machineOwnerBelongsToDeployment(machine, deploymentName) {
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

		result = append(result, detail)
	}

	return result, nil
}

// machineOwnerBelongsToDeployment checks if a machine belongs to a MachineDeployment.
func machineOwnerBelongsToDeployment(machine *clusterv1.Machine, deploymentName string) bool {
	// Machines are owned by MachineSets, which are owned by MachineDeployments
	// Check owner references to find the MachineSet
	for _, owner := range machine.OwnerReferences {
		if owner.Kind == "MachineSet" {
			// The MachineSet name typically starts with the deployment name
			// Format: <deployment-name>-<hash>
			if len(owner.Name) > len(deploymentName) && owner.Name[:len(deploymentName)] == deploymentName {
				return true
			}
		}
	}

	return false
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
