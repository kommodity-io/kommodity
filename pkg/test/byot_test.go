package integration_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	byotNamespace        = "default"
	byotNodeReadyTimeout = 10 * time.Minute
	byotDeleteTimeout    = 5 * time.Minute
	byotConditionTimeout = 5 * time.Minute
	byotHostPhaseTimeout = 5 * time.Minute
	byotNodePollInterval = 5 * time.Second
	byotControlPlanePort = 6443

	// byotScopeLabel is the operator (non-byot.io/) label the test sets on its
	// ByotHosts and includes in both pools' hostSelector, so the chart claims
	// only this test's hosts. byot.io/* labels are controller-managed and
	// cannot be pre-set, so a non-byot.io/ label is the scoping key.
	byotScopeLabel = "kommodity-test/cluster"

	// byotRoleLabel tags a ByotHost as control-plane or worker so the CP pool
	// hostSelector can claim only the designated CP host. With role labels + a
	// per-pool selector, the CP host is deterministic, so the controlPlaneEndpoint
	// can safely be its IP (no LB in Talos-in-Docker). Operator (non-byot.io/)
	// role labels are a legit prod pattern: operators tag hosts by role/pool and
	// selectors match.
	byotRoleLabel  = "kommodity-test/role"
	byotRoleCP     = "cp"
	byotRoleWorker = "worker"
)

// TestByotClusterFreshAdopt verifies the full host-registry lifecycle using the
// production hostSelector claim path (no hostRef pinning):
//
//  1. Talos containers boot in maintenance mode.
//  2. ByotHost objects are created (with a scoping operator label) and the host
//     controller discovers them and promotes them to Available.
//  3. The test reads the discovered byot.io/ hardware labels and builds a
//     resources/os.disk selector matching them (the same SKU path prod uses).
//  4. The chart is installed; ByotMachines claim matching Available hosts
//     (ByotHost phase Claimed) and adopt them (machine config applied, join).
//  5. The cluster becomes healthy (2 ready nodes).
//  6. Uninstalling the chart releases the hosts: each is reset to maintenance
//     and returns to Available (re-claimable), proving release-resets-to-
//     maintenance (Decision 12: no splitPolicy choice).
//  7. ByotHosts are deleted.
func TestByotClusterFreshAdopt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	clusterName := "byot-fresh"

	defer helpers.DumpByotMachines(ctx, env, byotNamespace)
	defer helpers.DumpByotHosts(ctx, env, byotNamespace)

	nodes := startByotHosts(t, clusterName)
	defer helpers.TerminateTalosNodes(t, nodes.CP, nodes.Worker)

	infra := buildByotInfraFromDiscoveredHosts(t, clusterName, nodes)

	installAndClaimCluster(t, clusterName, infra)
	assertClusterHealthy(t, clusterName, nodes)
	releaseAndCleanupHosts(t, clusterName, nodes)

	require.NoError(t, helpers.WaitForK8sResourceDeletion(
		env.KommodityCfg, byotNamespace, clusterName,
		machineGroup, machineVersion, "clusters", "", "", byotDeleteTimeout))
}

// byotHosts bundles the Talos containers and the ByotHost names claiming them.
type byotHosts struct {
	CP         helpers.TalosNode
	Worker     helpers.TalosNode
	CPHost     string
	WorkerHost string
}

// startByotHosts boots the Talos pair in maintenance mode, creates ByotHost
// records pointing at their internal docker IPs (with a scoping label), and
// waits for the host controller to discover + mark them Available.
func startByotHosts(t *testing.T, clusterName string) byotHosts {
	t.Helper()

	ctx := context.Background()
	talosVersion := helpers.GetTalosVersion(t)

	controlPlane, worker := helpers.StartTalosNodes(t, env, clusterName, talosVersion)

	require.Eventually(t, func() bool {
		return helpers.ProbeMaintenance(ctx, controlPlane.TalosAPIAddr, controlPlane.InternalIP) &&
			helpers.ProbeMaintenance(ctx, worker.TalosAPIAddr, worker.InternalIP)
	}, byotConditionTimeout, byotNodePollInterval, "nodes must start in maintenance mode")

	cpHost := clusterName + "-cp-host"
	workerHost := clusterName + "-worker-host"

	// ByotHost publicIP is the container IP on the shared docker network: the
	// kommodity container (running the host + machine controllers) reaches the
	// Talos maintenance API there. Discovery + adoption both happen in-process
	// inside kommodity, so the internal docker IP is the right address.
	// The scoping label lets this test's hostSelector claim only its own hosts
	// even when other byot tests run in parallel in the same namespace.
	scopeLabels := map[string]string{byotScopeLabel: clusterName}
	cpLabels := mergeLabels(scopeLabels, map[string]string{byotRoleLabel: byotRoleCP})
	workerLabels := mergeLabels(scopeLabels, map[string]string{byotRoleLabel: byotRoleWorker})

	helpers.CreateByotHost(t, env, cpHost, byotNamespace, controlPlane.InternalIP, cpLabels)
	helpers.CreateByotHost(t, env, workerHost, byotNamespace, worker.InternalIP, workerLabels)

	// Wait for the host controller to discover the maintenance API and mark
	// the hosts Available (claimable). Discovery runs Version/Memory/Disks/
	// Dmesg/LS over the maintenance API; liveness is confirmed first.
	helpers.WaitForByotHostPhase(t, env, cpHost, byotNamespace,
		helpers.ByotHostPhaseAvailable, byotHostPhaseTimeout)
	helpers.WaitForByotHostPhase(t, env, workerHost, byotNamespace,
		helpers.ByotHostPhaseAvailable, byotHostPhaseTimeout)

	return byotHosts{
		CP:         controlPlane,
		Worker:     worker,
		CPHost:     cpHost,
		WorkerHost: workerHost,
	}
}

// buildByotInfraFromDiscoveredHosts reads the byot.io/ hardware labels the host
// controller promoted from discovery and builds a ByotInfra whose
// resources/os.disk selector matches them (the prod SKU path). Both Talos
// containers are identical, so reading one host's labels suffices for both
// pools. The scoping operator label is included in the hostSelector so only
// this test's hosts are claimable.
func buildByotInfraFromDiscoveredHosts(t *testing.T, clusterName string, nodes byotHosts) helpers.ByotInfra {
	t.Helper()

	labels := helpers.ByotHostLabels(t, env, nodes.CPHost, byotNamespace)

	cpu := labels["byot.io/cpu-cores"]
	memory := labels["byot.io/memory-class"]
	diskType := labels["byot.io/disk-type"]
	diskSize := labels["byot.io/disk-class"]

	require.NotEmpty(t, cpu, "discovered byot.io/cpu-cores label must be set on %s", nodes.CPHost)
	require.NotEmpty(t, memory, "discovered byot.io/memory-class label must be set on %s", nodes.CPHost)
	require.NotEmpty(t, diskType, "discovered byot.io/disk-type label must be set on %s", nodes.CPHost)
	require.NotEmpty(t, diskSize, "discovered byot.io/disk-class label must be set on %s", nodes.CPHost)

	return helpers.ByotInfra{
		ControlPlaneEndpointHost:            nodes.CP.InternalIP,
		ControlPlaneEndpointPort:            byotControlPlanePort,
		CPU:                                 cpu,
		Memory:                              memory,
		DiskType:                            diskType,
		DiskSize:                            diskSize,
		HostSelectorMatchLabels:             map[string]string{byotScopeLabel: clusterName},
		ControlPlaneHostSelectorMatchLabels: map[string]string{byotRoleLabel: byotRoleCP},
	}
}

// installAndClaimCluster installs the chart with the discovered-SKU selector
// and waits for both hosts to be claimed by ByotMachines of the cluster.
func installAndClaimCluster(t *testing.T, clusterName string, infra helpers.ByotInfra) {
	t.Helper()

	helpers.InstallKommodityClusterChartByot(t, env, clusterName, byotNamespace, infra)

	// The chart renders ByotMachineTemplates with a hostSelector; CAPI clones a
	// ByotMachine per replica and the byot controller claims a matching
	// Available host via claimRef CAS. Wait for both test hosts to be claimed.
	helpers.WaitForByotHostPhase(t, env, clusterName+"-cp-host", byotNamespace,
		helpers.ByotHostPhaseClaimed, byotConditionTimeout)
	helpers.WaitForByotHostPhase(t, env, clusterName+"-worker-host", byotNamespace,
		helpers.ByotHostPhaseClaimed, byotConditionTimeout)

	// Each host's claimRef must name a ByotMachine belonging to this cluster.
	assertByotHostClaimedByClusterMachine(t, clusterName+"-cp-host", clusterName)
	assertByotHostClaimedByClusterMachine(t, clusterName+"-worker-host", clusterName)
}

// assertClusterHealthy waits for the CAPI kubeconfig secret and the expected
// ready node count in the workload cluster.
func assertClusterHealthy(t *testing.T, clusterName string, nodes byotHosts) {
	t.Helper()

	waitForKubeconfigSecret(t, clusterName)

	workloadClient := helpers.GetWorkloadClient(t, env, clusterName, byotNamespace, nodes.CP.APIServerURL)
	waitForNodeCount(t, workloadClient, 2)
}

// releaseAndCleanupHosts uninstalls the chart, asserts both hosts return to
// Available (reset to maintenance, claimRef cleared), then deletes the ByotHost
// records and waits for their deletion.
func releaseAndCleanupHosts(t *testing.T, clusterName string, nodes byotHosts) {
	t.Helper()

	ctx := context.Background()

	// Release: uninstall triggers CAPI cascade-delete of Machines → ByotMachines
	// release their host (reset to maintenance, claimRef cleared, phase Releasing)
	// → the host liveness loop flips it back to Available.
	helpers.UninstallKommodityClusterChart(t, env, clusterName, byotNamespace)

	// Confirm the machine layer is gone (ByotMachines deleted by CAPI cascade).
	helpers.WaitForClusterByotMachinesDeletion(t, env, clusterName, byotNamespace, byotDeleteTimeout)

	// Both hosts must return to Available (re-claimable), proving
	// release-resets-to-maintenance.
	helpers.WaitForByotHostPhase(t, env, nodes.CPHost, byotNamespace,
		helpers.ByotHostPhaseAvailable, byotHostPhaseTimeout)
	helpers.WaitForByotHostPhase(t, env, nodes.WorkerHost, byotNamespace,
		helpers.ByotHostPhaseAvailable, byotHostPhaseTimeout)

	// claimRef cleared on release.
	assert.Empty(t, helpers.ByotHostClaimRefName(t, env, nodes.CPHost, byotNamespace),
		"CP host claimRef must be cleared after release")
	assert.Empty(t, helpers.ByotHostClaimRefName(t, env, nodes.WorkerHost, byotNamespace),
		"worker host claimRef must be cleared after release")

	// The hosts are back in maintenance mode (reset wiped the cluster config).
	require.True(t,
		helpers.ProbeMaintenance(ctx, nodes.CP.TalosAPIAddr, nodes.CP.InternalIP),
		"CP host must be back in maintenance mode after release")
	require.True(t,
		helpers.ProbeMaintenance(ctx, nodes.Worker.TalosAPIAddr, nodes.Worker.InternalIP),
		"worker host must be back in maintenance mode after release")

	// Cleanup the ByotHost records (finalizers are gone now the claim is released).
	helpers.DeleteByotHost(t, env, nodes.CPHost, byotNamespace)
	helpers.DeleteByotHost(t, env, nodes.WorkerHost, byotNamespace)

	helpers.WaitForByotHostDeletion(t, env, nodes.CPHost, byotNamespace, byotDeleteTimeout)
	helpers.WaitForByotHostDeletion(t, env, nodes.WorkerHost, byotNamespace, byotDeleteTimeout)
}

// assertByotHostClaimedByClusterMachine verifies the ByotHost's claimRef names
// a ByotMachine carrying the cluster-name label (CAPI clones infra machines
// with Name == Machine.Name, and the chart labels them cluster.x-k8s.io/
// cluster-name). The exact Machine name is controller-generated, so match by
// label rather than by hard-coded name.
func assertByotHostClaimedByClusterMachine(t *testing.T, hostName string, clusterName string) {
	t.Helper()

	require.Eventually(t, func() bool {
		claimName := helpers.ByotHostClaimRefName(t, env, hostName, byotNamespace)
		if claimName == "" {
			return false
		}

		return helpers.ByotMachineExistsForCluster(t, env, claimName, byotNamespace, clusterName)
	}, byotConditionTimeout, byotNodePollInterval,
		"ByotHost %s must be claimed by a ByotMachine of cluster %s", hostName, clusterName)
}

// mergeLabels combines two label maps (override winning on key conflict).
// Returns a new map (nil when both empty).
func mergeLabels(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	maps.Copy(out, override)

	return out
}

func waitForKubeconfigSecret(t *testing.T, clusterName string) {
	t.Helper()

	require.NoError(t, helpers.WaitForK8sResourceCreation(
		env.KommodityCfg, byotNamespace, clusterName+"-kubeconfig",
		"", "v1", "secrets", "", "", byotConditionTimeout, 1))
}

// waitForNodeCount waits until the workload cluster reports the expected Ready
// node count.
func waitForNodeCount(t *testing.T, client *kubernetes.Clientset, expected int) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(byotNodeReadyTimeout)

	for {
		nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err == nil && countReadyNodes(nodes.Items) == expected {
			return
		}

		if time.Now().After(deadline) {
			got := 0

			nodes, listErr := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if listErr == nil {
				got = countReadyNodes(nodes.Items)
			}

			require.Failf(t, "timed out waiting for workload nodes",
				"expected %d ready nodes, got %d (list err: %v)", expected, got, err)
		}

		time.Sleep(byotNodePollInterval)
	}
}

// countReadyNodes counts nodes with NodeReady=True.
func countReadyNodes(nodes []corev1.Node) int {
	ready := 0

	for _, node := range nodes {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}

	return ready
}
