package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	byotNamespace            = "default"
	byotJoinPreflightCond    = "JoinPreflight"
	byotReasonBundleMatch    = "BundleMatch"
	byotReasonNoCredentials  = "NoCredentials"
	byotReasonBundleMismatch = "BundleMismatch"
	byotNodeReadyTimeout     = 10 * time.Minute
	byotDeleteTimeout        = 5 * time.Minute
	byotConditionTimeout     = 5 * time.Minute
	byotJoinPolicyReset      = "Reset"
	byotSplitPolicyReset     = "Reset"
	byotSplitPolicyNone      = "None"
	byotNodePollInterval     = 5 * time.Second
)

func waitForKubeconfigSecret(t *testing.T, clusterName string) {
	t.Helper()

	require.NoError(t, helpers.WaitForK8sResourceCreation(
		env.KommodityCfg, byotNamespace, clusterName+"-kubeconfig",
		"", "v1", "secrets", "", "", byotConditionTimeout, 1))
}

// TestByotClusterFreshAdopt verifies fresh adoption: maintenance-mode nodes get
// a fresh machine config applied and join the workload cluster.
func TestByotClusterFreshAdopt(t *testing.T) {
	t.Parallel()

	clusterName := "byot-fresh"
	nodes := startFreshByotCluster(t, clusterName, helpers.ByotInfra{})

	defer helpers.TerminateTalosNodes(t, nodes.CP, nodes.Worker)

	helpers.UninstallKommodityClusterChart(t, env, clusterName, byotNamespace)
}

// TestByotClusterSplitNoneRoundTrip verifies Split=None is lossless: deleting a
// worker Machine leaves the host untouched (still carrying the cluster bundle),
// and re-creating the Machine re-adopts it via BundleMatch.
func TestByotClusterSplitNoneRoundTrip(t *testing.T) {
	t.Parallel()

	clusterName := "byot-roundtrip"
	nodes := startFreshByotCluster(t, clusterName, helpers.ByotInfra{
		SplitPolicy:       byotSplitPolicyNone,
		WorkerSplitPolicy: byotSplitPolicyNone,
	})

	defer helpers.TerminateTalosNodes(t, nodes.CP, nodes.Worker)

	workloadClient := helpers.GetWorkloadClient(t, env, clusterName, byotNamespace, nodes.APIServerURL)

	workerMachine := nodes.WorkerMachineName

	deleteCAPIMachine(t, workerMachine, byotNamespace)
	require.NoError(t, helpers.WaitForK8sResourceDeletion(
		env.KommodityCfg, byotNamespace, workerMachine,
		machineGroup, machineVersion, machineResource, "", "", byotDeleteTimeout))

	waitForNodeCount(t, workloadClient, 1)
	assertHostKeepsBundle(t, nodes.Worker, clusterName)

	helpers.UpgradeKommodityClusterChartByot(t, env, clusterName, byotNamespace, nodes.Infra)

	// The recreated ByotMachine can only join once the join preflight accepted
	// the old bundle; once adopted the preflight condition is frozen.
	waitForNodeCount(t, workloadClient, 2)

	status, reason := helpers.ByotMachineConditionState(
		t, env, byotNamespace, workerMachine, byotJoinPreflightCond)
	assert.Equal(t, corev1.ConditionTrue, status, "join preflight must have been accepted on re-adopt")
	assert.Equal(t, byotReasonBundleMatch, reason)

	helpers.UninstallKommodityClusterChart(t, env, clusterName, byotNamespace)
}

// TestByotClusterJoinBlockedThenReset : a machine carrying
// a foreign bundle is blocked by the join preflight without and with
// credentials, and only wiped once joinPolicy=Reset is set.
func TestByotClusterJoinBlockedThenReset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	defer helpers.DumpByotMachines(ctx, env, byotNamespace)

	clusterA := "byot-victim"
	victim := startFreshByotCluster(t, clusterA, helpers.ByotInfra{
		SplitPolicy:       byotSplitPolicyNone,
		WorkerSplitPolicy: byotSplitPolicyNone,
	})

	defer helpers.TerminateTalosNodes(t, victim.CP, victim.Worker)

	victimTalosconfig := helpers.GetClusterTalosconfig(t, env, clusterA, byotNamespace)

	helpers.UninstallKommodityClusterChart(t, env, clusterA, byotNamespace)

	require.NoError(t, helpers.WaitForK8sResourceDeletion(
		env.KommodityCfg, byotNamespace, clusterA,
		machineGroup, machineVersion, "clusters", "", "", byotDeleteTimeout))

	require.Eventually(t, func() bool {
		return helpers.ProbeAuthenticated(ctx, victim.Worker.TalosAPIAddr, victim.Worker.InternalIP, victimTalosconfig) &&
			!helpers.ProbeMaintenance(ctx, victim.Worker.TalosAPIAddr, victim.Worker.InternalIP)
	}, byotConditionTimeout, 5*time.Second,
		"worker must still carry victim cluster's bundle after Split=None")

	clusterB := "byot-rescue"
	rescueInfra := victim.Infra
	rescueInfra.ControlPlaneName = clusterB + "-cp0"
	rescueInfra.WorkerName = clusterB + "-worker0"

	helpers.InstallKommodityClusterChartByot(t, env, clusterB, byotNamespace, rescueInfra)
	defer helpers.UninstallKommodityClusterChart(t, env, clusterB, byotNamespace)

	workerMachine := byotWorkerMachineName(clusterB, rescueInfra.WorkerName)
	reason, _ := helpers.WaitForByotMachineCondition(
		t, env, byotNamespace, workerMachine,
		byotJoinPreflightCond, corev1.ConditionFalse, byotConditionTimeout)
	assert.Equal(t, byotReasonNoCredentials, reason)

	secretName := "foreign-talosconfig"
	helpers.CreateTalosconfigSecret(t, env, secretName, byotNamespace, victimTalosconfig)

	rescueInfra.WorkerTalosConfigRef = secretName
	rescueInfra.TalosConfigRef = secretName
	helpers.UpgradeKommodityClusterChartByot(t, env, clusterB, byotNamespace, rescueInfra)

	reason, _ = helpers.WaitForByotMachineCondition(
		t, env, byotNamespace, workerMachine,
		byotJoinPreflightCond, corev1.ConditionFalse, byotConditionTimeout)
	assert.Equal(t, byotReasonBundleMismatch, reason)

	rescueInfra.WorkerJoinPolicy = byotJoinPolicyReset
	rescueInfra.JoinPolicy = byotJoinPolicyReset
	helpers.UpgradeKommodityClusterChartByot(t, env, clusterB, byotNamespace, rescueInfra)

	reason, _ = helpers.WaitForByotMachineCondition(
		t, env, byotNamespace, workerMachine,
		"ResetPerformed", corev1.ConditionFalse, byotConditionTimeout)
	assert.Equal(t, "ResetFailed", reason,
		"joinPolicy=Reset must attempt a wipe; on docker (no block devices) the wipe fails")
}

// TestByotClusterSplitResetWipesMachines verifies Split=Reset: deleting the
// cluster issues wipe attempts. Talos-in-docker has no block devices, so the
// wipe stays blocked (ResetFailed, retrying) and the hosts keep the cluster
// bundle until pre-cleaned manually.
func TestByotClusterSplitResetWipesMachines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clusterName := "byot-splitreset"
	infra := helpers.ByotInfra{
		SplitPolicy:       byotSplitPolicyReset,
		WorkerSplitPolicy: byotSplitPolicyReset,
	}

	nodes := startFreshByotCluster(t, clusterName, infra)
	defer helpers.TerminateTalosNodes(t, nodes.CP, nodes.Worker)
	defer helpers.DumpByotMachines(ctx, env, byotNamespace)

	clusterTalosconfig := helpers.GetClusterTalosconfig(t, env, clusterName, byotNamespace)

	helpers.UninstallKommodityClusterChart(t, env, clusterName, byotNamespace)

	require.Eventually(t, func() bool {
		return helpers.ByotMachineTerminating(ctx, env, byotNamespace, nodes.CPMachineName) &&
			helpers.ByotMachineTerminating(ctx, env, byotNamespace, nodes.WorkerMachineName)
	}, byotDeleteTimeout, 5*time.Second, "BYOT machines must be terminating after uninstall")

	// Split=Reset retries the wipe silently on the deletion path (no
	// ResetFailed condition). On docker the wipe cannot succeed (no block
	// devices); the machines stay stuck terminating and keep the bundle.
	require.Eventually(t, func() bool {
		return helpers.ProbeAuthenticated(ctx, nodes.Worker.TalosAPIAddr, nodes.Worker.InternalIP, clusterTalosconfig) &&
			!helpers.ProbeMaintenance(ctx, nodes.Worker.TalosAPIAddr, nodes.Worker.InternalIP)
	}, byotConditionTimeout, 5*time.Second,
		"host must still carry the cluster bundle while the wipe cannot complete")

	helpers.CleanupTerminatingByotMachines(ctx, env, byotNamespace,
		nodes.CPMachineName, nodes.WorkerMachineName)
}

// byotNodes bundles everything a test needs about the started node pair.
type byotNodes struct {
	CP                helpers.TalosNode
	Worker            helpers.TalosNode
	Infra             helpers.ByotInfra
	APIServerURL      string
	CPMachineName     string
	WorkerMachineName string
}

// byotCPMachineName mirrors the chart's "<release>-cp-<key>" naming for control
// plane ByotMachines (templates/provider/byot/machines.yaml).
func byotCPMachineName(releaseName string, key string) string {
	return releaseName + "-cp-" + key
}

// byotWorkerMachineName mirrors "<release>-worker-<nodepool>-<key>"; the byot
// values file always uses the "default" nodepool.
func byotWorkerMachineName(releaseName string, key string) string {
	return releaseName + "-worker-default-" + key
}

// startFreshByotCluster boots the Talos pair in maintenance mode, installs the
// helm chart, and blocks until both ByotMachines passed the join preflight.
func startFreshByotCluster(t *testing.T, clusterName string, infra helpers.ByotInfra) byotNodes {
	t.Helper()

	ctx := context.Background()
	talosVersion := helpers.GetTalosVersion(t)

	controlPlane, worker := helpers.StartTalosNodes(t, env, clusterName, talosVersion)

	cpName := clusterName + "-cp0"
	workerName := clusterName + "-worker0"

	if infra.ControlPlaneName == "" {
		infra.ControlPlaneName = cpName
	}

	if infra.WorkerName == "" {
		infra.WorkerName = workerName
	}

	infra.ControlPlaneIP = controlPlane.InternalIP
	infra.WorkerIP = worker.InternalIP

	require.Eventually(t, func() bool {
		return helpers.ProbeMaintenance(ctx, controlPlane.TalosAPIAddr, controlPlane.InternalIP) &&
			helpers.ProbeMaintenance(ctx, worker.TalosAPIAddr, worker.InternalIP)
	}, byotConditionTimeout, byotNodePollInterval, "nodes must start in maintenance mode")

	helpers.InstallKommodityClusterChartByot(t, env, clusterName, byotNamespace, infra)

	cpMachineName := byotCPMachineName(clusterName, infra.ControlPlaneName)
	workerMachineName := byotWorkerMachineName(clusterName, infra.WorkerName)

	waitForKubeconfigSecret(t, clusterName)

	workloadClient := helpers.GetWorkloadClient(t, env, clusterName, byotNamespace, controlPlane.APIServerURL)
	waitForNodeCount(t, workloadClient, 2)

	return byotNodes{
		CP:                controlPlane,
		Worker:            worker,
		Infra:             infra,
		APIServerURL:      controlPlane.APIServerURL,
		CPMachineName:     cpMachineName,
		WorkerMachineName: workerMachineName,
	}
}

// deleteCAPIMachine deletes a CAPI Machine object to trigger the split flow.
func deleteCAPIMachine(t *testing.T, machineName string, namespace string) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	gvr := schema.GroupVersionResource{
		Group:    machineGroup,
		Version:  machineVersion,
		Resource: machineResource,
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(
		context.Background(), machineName, metav1.DeleteOptions{})
	require.NoError(t, err)
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

// assertHostKeepsBundle verifies a node still carries the given cluster's
// bundle: it answers the Talos API with that cluster's credentials and is NOT
// back in maintenance mode. talosAPIAddr is the host-mapped Talos endpoint.
func assertHostKeepsBundle(t *testing.T, node helpers.TalosNode, clusterName string) {
	t.Helper()

	talosconfig := helpers.GetClusterTalosconfig(t, env, clusterName, byotNamespace)

	ctx := context.Background()

	require.Eventually(t, func() bool {
		return helpers.ProbeAuthenticated(ctx, node.TalosAPIAddr, node.InternalIP, talosconfig) &&
			!helpers.ProbeMaintenance(ctx, node.TalosAPIAddr, node.InternalIP)
	}, byotConditionTimeout, byotNodePollInterval,
		"host %s must still answer with the cluster's talosconfig after Split=None", node.InternalIP)
	assert.False(t,
		helpers.ProbeMaintenance(ctx, node.TalosAPIAddr, node.InternalIP),
		"host %s must not be in maintenance mode after Split=None", node.InternalIP)
}
