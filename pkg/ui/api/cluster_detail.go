package api

import (
	"context"
	"fmt"
	"strings"

	taloscontrolplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Machine health status values for the UI health circle.
const (
	MachineHealthHealthy     = "Healthy"
	MachineHealthUnhealthy   = "Unhealthy"
	MachineHealthCheckFailed = "CheckFailed"
	MachineHealthUnknown     = "Unknown"
)

// ClusterDetail holds detailed information about a cluster.
type ClusterDetail struct {
	ClusterInfo

	ControlPlane       *ControlPlaneDetail
	MachineDeployments []MachineDeploymentDetail
}

// ControlPlaneDetail holds information about a control plane.
type ControlPlaneDetail struct {
	Name     string
	Phase    string
	Replicas *int32
	Machines []MachineDetail
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
	CreationTime      string
	Phase             string
	KubernetesVersion string
	Health            string
	HealthReason      string
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

	// Fetch all machines for the cluster once and group them.
	machineList := &clusterv1.MachineList{}

	err = client.List(ctx, machineList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	machinesByDeployment, controlPlaneMachines := groupMachines(machineList)

	// Get MachineDeployments
	machineDeployments, err := getMachineDeployments(ctx, client, clusterName, machinesByDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine deployments: %w", err)
	}

	// Get control plane
	controlPlane, err := getControlPlane(ctx, client, clusterName, controlPlaneMachines, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane: %w", err)
	}

	return &ClusterDetail{
		ClusterInfo:        *clusterInfo,
		ControlPlane:       controlPlane,
		MachineDeployments: machineDeployments,
	}, nil
}

// getControlPlane retrieves the TalosControlPlane for the cluster and its machines.
func getControlPlane(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	machines []MachineDetail,
	logger *zap.Logger,
) (*ControlPlaneDetail, error) {
	cpList := &taloscontrolplanev1.TalosControlPlaneList{}

	err := client.List(ctx, cpList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list control planes: %w", err)
	}

	if len(cpList.Items) == 0 {
		logger.Debug("No control plane found for cluster", zap.String("cluster", clusterName))

		if len(machines) == 0 {
			return nil, nil //nolint:nilnil
		}

		return &ControlPlaneDetail{
			Name:     clusterName,
			Phase:    UnknownVersion,
			Machines: machines,
		}, nil
	}

	controlPlane := &cpList.Items[0]

	phase := UnknownVersion

	switch {
	case controlPlane.Status.Ready:
		phase = MachinePhaseRunning
	case controlPlane.Status.Initialized:
		phase = MachinePhaseProvisioning
	}

	replicas := controlPlane.Status.Replicas

	return &ControlPlaneDetail{
		Name:     controlPlane.Name,
		Phase:    phase,
		Replicas: &replicas,
		Machines: machines,
	}, nil
}

// getMachineDeployments retrieves all MachineDeployments for a cluster.
func getMachineDeployments(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	machinesByDeployment map[string][]MachineDetail,
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

		// Get machines for this deployment from the grouped map
		machines := machinesByDeployment[deployment.Name]

		phase := deployment.Status.Phase
		if phase == "" {
			phase = UnknownVersion
		}

		replicas := deployment.Status.Replicas

		detail := MachineDeploymentDetail{
			Name:     deployment.Name,
			Phase:    phase,
			Replicas: &replicas,
			MinSize:  minSize,
			MaxSize:  maxSize,
			Machines: machines,
		}

		result = append(result, detail)
	}

	return result, nil
}

// groupMachines splits the machine list into per-MachineDeployment groups and a control plane group.
func groupMachines(machineList *clusterv1.MachineList) (map[string][]MachineDetail, []MachineDetail) {
	byDeployment := make(map[string][]MachineDetail)

	var controlPlane []MachineDetail

	for i := range machineList.Items {
		machine := &machineList.Items[i]
		detail := machineToDetail(machine)

		if _, isControlPlane := machine.Labels[clusterv1.MachineControlPlaneLabel]; isControlPlane {
			controlPlane = append(controlPlane, detail)

			continue
		}

		deploymentName := getDeploymentNameFromMachine(machine)
		if deploymentName == "" {
			continue
		}

		byDeployment[deploymentName] = append(byDeployment[deploymentName], detail)
	}

	return byDeployment, controlPlane
}

// machineToDetail converts a CAPI Machine into a UI MachineDetail.
func machineToDetail(machine *clusterv1.Machine) MachineDetail {
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

	health, healthReason := machineHealthFromConditions(machine)

	return MachineDetail{
		Name:              machine.Name,
		NodeName:          nodeName,
		CreationTime:      machine.CreationTimestamp.Format("2006-01-02 15:04:05"),
		Phase:             phase,
		KubernetesVersion: kubernetesVersion,
		Health:            health,
		HealthReason:      healthReason,
	}
}

// healthSummary aggregates the relevant CAPI conditions for a machine.
type healthSummary struct {
	anyFalse   *clusterv1.Condition
	anyUnknown *clusterv1.Condition
	sawTrue    bool
	sawAny     bool
}

// summarizeHealthConditions walks machine conditions and aggregates those relevant to health.
func summarizeHealthConditions(machine *clusterv1.Machine) healthSummary {
	relevant := map[clusterv1.ConditionType]bool{
		clusterv1.MachineHealthCheckSucceededCondition: true,
		clusterv1.MachineNodeHealthyCondition:          true,
	}

	var summary healthSummary

	for i := range machine.Status.Conditions {
		cond := &machine.Status.Conditions[i]
		if !relevant[cond.Type] {
			continue
		}

		summary.sawAny = true

		switch cond.Status {
		case corev1.ConditionFalse:
			if summary.anyFalse == nil {
				summary.anyFalse = cond
			}
		case corev1.ConditionUnknown:
			if summary.anyUnknown == nil {
				summary.anyUnknown = cond
			}
		case corev1.ConditionTrue:
			summary.sawTrue = true
		}
	}

	return summary
}

// machineHealthFromConditions derives a UI health state from CAPI conditions.
// Considers both HealthCheckSucceeded (set by MachineHealthCheck) and NodeHealthy
// (reflects backing node Ready status). NodeHealthy is used because MHC may not be
// configured, in which case HealthCheckSucceeded is absent.
func machineHealthFromConditions(machine *clusterv1.Machine) (string, string) {
	summary := summarizeHealthConditions(machine)

	switch {
	case summary.anyFalse != nil:
		return MachineHealthUnhealthy, summary.anyFalse.Message
	case summary.anyUnknown != nil:
		return MachineHealthCheckFailed, summary.anyUnknown.Message
	case summary.sawTrue:
		return MachineHealthHealthy, ""
	}

	return MachineHealthUnknown, ""
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
