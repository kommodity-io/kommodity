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
	Name            string
	Phase           string
	Replicas        *int32
	HealthyMachines int
	Machines        []MachineDetail
}

// MachineDeploymentDetail holds information about a MachineDeployment.
type MachineDeploymentDetail struct {
	Name            string
	Phase           string
	Replicas        *int32
	MinSize         *int32
	MaxSize         *int32
	HealthyMachines int
	Machines        []MachineDetail
}

// MachineDetail holds information about a Machine.
type MachineDetail struct {
	Name              string
	NodeName          string
	CreationTime      string
	Phase             string
	KubernetesVersion string
	Health            string
	Conditions        []MachineConditionDetail
}

// MachineConditionDetail holds a single condition for UI display.
type MachineConditionDetail struct {
	Type   string
	Status string
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
			Name:            clusterName,
			Phase:           UnknownVersion,
			HealthyMachines: countHealthyMachines(machines),
			Machines:        machines,
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
		Name:            controlPlane.Name,
		Phase:           phase,
		Replicas:        &replicas,
		HealthyMachines: countHealthyMachines(machines),
		Machines:        machines,
	}, nil
}

// countHealthyMachines returns the number of machines with Health == Healthy.
func countHealthyMachines(machines []MachineDetail) int {
	count := 0

	for _, machine := range machines {
		if machine.Health == MachineHealthHealthy {
			count++
		}
	}

	return count
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
			Name:            deployment.Name,
			Phase:           phase,
			Replicas:        &replicas,
			MinSize:         minSize,
			MaxSize:         maxSize,
			HealthyMachines: countHealthyMachines(machines),
			Machines:        machines,
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

	return MachineDetail{
		Name:              machine.Name,
		NodeName:          nodeName,
		CreationTime:      machine.CreationTimestamp.Format("2006-01-02 15:04:05"),
		Phase:             phase,
		KubernetesVersion: kubernetesVersion,
		Health:            machineHealthFromConditions(machine),
		Conditions:        extractDisplayConditions(machine),
	}
}

// displayConditionTypes returns the condition types surfaced in the UI tooltip,
// in display order.
func displayConditionTypes() []clusterv1.ConditionType {
	return []clusterv1.ConditionType{
		clusterv1.ReadyCondition,
		clusterv1.BootstrapReadyCondition,
		clusterv1.InfrastructureReadyCondition,
		clusterv1.MachineNodeHealthyCondition,
		clusterv1.MachineHealthCheckSucceededCondition,
	}
}

// extractDisplayConditions returns the configured display conditions for a machine,
// preserving the order defined by displayConditionTypes. Missing conditions are
// rendered with status "Unknown".
func extractDisplayConditions(machine *clusterv1.Machine) []MachineConditionDetail {
	found := make(map[clusterv1.ConditionType]*clusterv1.Condition, len(machine.Status.Conditions))

	for i := range machine.Status.Conditions {
		cond := &machine.Status.Conditions[i]
		found[cond.Type] = cond
	}

	types := displayConditionTypes()
	result := make([]MachineConditionDetail, 0, len(types))

	for _, condType := range types {
		cond, ok := found[condType]
		if !ok {
			result = append(result, MachineConditionDetail{
				Type:   string(condType),
				Status: string(corev1.ConditionUnknown),
			})

			continue
		}

		result = append(result, MachineConditionDetail{
			Type:   string(condType),
			Status: string(cond.Status),
		})
	}

	return result
}

// healthSummary aggregates the relevant CAPI condition statuses for a machine.
type healthSummary struct {
	hasFalse   bool
	hasUnknown bool
	hasTrue    bool
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

		switch cond.Status {
		case corev1.ConditionFalse:
			summary.hasFalse = true
		case corev1.ConditionUnknown:
			summary.hasUnknown = true
		case corev1.ConditionTrue:
			summary.hasTrue = true
		}
	}

	return summary
}

// machineHealthFromConditions derives a UI health state from CAPI conditions.
// Considers both HealthCheckSucceeded (set by MachineHealthCheck) and NodeHealthy
// (reflects backing node Ready status). NodeHealthy is used because MHC may not be
// configured, in which case HealthCheckSucceeded is absent.
func machineHealthFromConditions(machine *clusterv1.Machine) string {
	summary := summarizeHealthConditions(machine)

	switch {
	case summary.hasFalse:
		return MachineHealthUnhealthy
	case summary.hasUnknown:
		return MachineHealthCheckFailed
	case summary.hasTrue:
		return MachineHealthHealthy
	}

	return MachineHealthUnknown
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
