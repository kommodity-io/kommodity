package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	taloscontrolplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
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
	Zone              string
	CreationTime      string
	Age               string
	Phase             string
	KubernetesVersion string
	Health            string
	Outdated          bool
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
	// Degrade gracefully on list failure: log and render with no machines rather than fail the page.
	machineList := &clusterv1.MachineList{}

	err = client.List(ctx, machineList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		logger.Warn("Failed to list machines",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)

		machineList = &clusterv1.MachineList{}
	}

	// List MachineDeployments and MachineSets once; both are reused to detect
	// outdated MachineSets and to build the deployment details.
	mdList, msList, err := listDeploymentResources(ctx, client, clusterName, logger)
	if err != nil {
		return nil, err
	}

	currentMachineSets := currentMachineSetNames(mdList, msList)

	machinesByDeployment, controlPlaneMachines := groupMachines(machineList, currentMachineSets)

	// Get MachineDeployments
	machineDeployments := getMachineDeployments(mdList, machinesByDeployment)

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

// getMachineDeployments builds MachineDeployment details from a pre-fetched list.
func getMachineDeployments(
	mdList *clusterv1.MachineDeploymentList,
	machinesByDeployment map[string][]MachineDetail,
) []MachineDeploymentDetail {
	result := make([]MachineDeploymentDetail, 0, len(mdList.Items))

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

	return result
}

// listDeploymentResources fetches all MachineDeployments and MachineSets for a cluster.
// MachineSet listing is best-effort: on failure an empty list is returned so the UI
// degrades gracefully (no outdated detection) instead of erroring the whole page.
func listDeploymentResources(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	logger *zap.Logger,
) (*clusterv1.MachineDeploymentList, *clusterv1.MachineSetList, error) {
	mdList := &clusterv1.MachineDeploymentList{}

	err := client.List(ctx, mdList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list machine deployments: %w", err)
	}

	msList := &clusterv1.MachineSetList{}

	err = client.List(ctx, msList, ctrlclient.InNamespace(DefaultNamespace), ctrlclient.MatchingLabels{
		ClusterNameLabel: clusterName,
	})
	if err != nil {
		logger.Warn("Failed to list machine sets",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)

		msList = &clusterv1.MachineSetList{}
	}

	return mdList, msList, nil
}

// currentMachineSetNames returns the set of MachineSet names whose template matches
// the current template of their owning MachineDeployment. A MachineSet is "current"
// when its spec template is semantically equal to the deployment's template; all
// other MachineSets owned by the same deployment are stale and their machines are
// pending rollout.
func currentMachineSetNames(
	mdList *clusterv1.MachineDeploymentList,
	msList *clusterv1.MachineSetList,
) map[string]struct{} {
	current := make(map[string]struct{})

	deploymentByName := make(map[string]*clusterv1.MachineDeployment, len(mdList.Items))
	for i := range mdList.Items {
		deploymentByName[mdList.Items[i].Name] = &mdList.Items[i]
	}

	for i := range msList.Items {
		machineSet := &msList.Items[i]

		deploymentName, ok := getDeploymentNameFromMachineSet(machineSet)
		if !ok {
			continue
		}

		deployment, found := deploymentByName[deploymentName]
		if !found {
			continue
		}

		if apiequality.Semantic.DeepEqual(machineSet.Spec.Template, deployment.Spec.Template) {
			current[machineSet.Name] = struct{}{}
		}
	}

	return current
}

// groupMachines splits the machine list into per-MachineDeployment groups and a control plane group.
// currentMachineSets identifies MachineSets matching their deployment's current template; machines
// belonging to any other MachineSet are flagged as outdated.
func groupMachines(
	machineList *clusterv1.MachineList,
	currentMachineSets map[string]struct{},
) (map[string][]MachineDetail, []MachineDetail) {
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

		if machineSetName := getMachineSetNameFromMachine(machine); machineSetName != "" {
			if _, isCurrent := currentMachineSets[machineSetName]; !isCurrent {
				detail.Outdated = true
			}
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

	// No failureDomain means the pool has no zones set; the provider uses its default zone.
	zone := "—"
	if machine.Spec.FailureDomain != nil && *machine.Spec.FailureDomain != "" {
		zone = *machine.Spec.FailureDomain
	}

	return MachineDetail{
		Name:              machine.Name,
		NodeName:          nodeName,
		Zone:              zone,
		CreationTime:      machine.CreationTimestamp.Format(time.RFC3339),
		Age:               FormatAge(machine.CreationTimestamp.Time),
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

// getMachineSetNameFromMachine returns the MachineSet name owning a machine, preferring
// the CAPI set-name label (handles long names that are hashed) and falling back to the
// MachineSet owner reference.
func getMachineSetNameFromMachine(machine *clusterv1.Machine) string {
	if name, ok := machine.Labels[clusterv1.MachineSetNameLabel]; ok && name != "" {
		return name
	}

	for _, owner := range machine.OwnerReferences {
		if owner.Kind == "MachineSet" {
			return owner.Name
		}
	}

	return ""
}

// getDeploymentNameFromMachineSet returns the MachineDeployment name owning a MachineSet,
// using the deployment-name label first, then the owner reference. The second return is
// false when the MachineSet is not owned by a MachineDeployment.
func getDeploymentNameFromMachineSet(machineSet *clusterv1.MachineSet) (string, bool) {
	if name, ok := machineSet.Labels[clusterv1.MachineDeploymentNameLabel]; ok && name != "" {
		return name, true
	}

	for _, owner := range machineSet.OwnerReferences {
		if owner.Kind == "MachineDeployment" {
			return owner.Name, true
		}
	}

	return "", false
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
