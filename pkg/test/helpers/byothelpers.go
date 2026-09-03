package helpers

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"maps"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	byotValuesFile      = "values.byot.yaml"
	byotTalosAPIPort    = "50000/tcp"
	byotKubeAPIPort     = "6443/tcp"
	byotTalosImageRepo  = "ghcr.io/siderolabs/talos"
	talosProbeTimeout   = 10 * time.Second
	talosStartupTimeout = 3 * time.Minute
	byotPollInterval    = 5 * time.Second
)

// ByotInfra carries Helm value overrides for BYOT (Talos-in-Docker) clusters
// under the host-registry model. The chart claims ByotHost objects (created
// out of band by the test) by label hostSelector — the same path production
// clusters use (resources/os.disk mapped to the curated byot.io/ labels the
// host controller promotes from discovery).
type ByotInfra struct {
	// ControlPlaneEndpointHost is the control-plane API endpoint (VIP/LB or a
	// committed CP host). For Talos-in-Docker this is the CP container's IP on
	// the shared docker network (reachable by the worker container).
	ControlPlaneEndpointHost string
	// ControlPlaneEndpointPort is the control-plane API endpoint port (6443).
	ControlPlaneEndpointPort int
	// CPU is the resources.cpu value (plain integer string of physical cores)
	// mapped to the byot.io/cpu-cores selector label.
	CPU string
	// Memory is the resources.memory bucket (4G 8G 16G 32G 64G 128G) mapped to
	// the byot.io/memory-class selector label.
	Memory string
	// DiskType is the os.disk.type (nvme ssd hdd sd) mapped to byot.io/disk-type.
	DiskType string
	// DiskSize is the os.disk.size bucket (20G 100G 250G 500G 1T) mapped to
	// byot.io/disk-class.
	DiskSize string
	// HostSelectorMatchLabels is an optional freeform operator label selector
	// merged on top of the derived byot.io/ labels. Used here to scope a test's
	// hosts to its own cluster (a non-byot.io/ label the test sets on its
	// ByotHosts), so parallel byot tests cannot claim each other's hosts.
	HostSelectorMatchLabels map[string]string
	// ControlPlaneHostSelectorMatchLabels extends HostSelectorMatchLabels with
	// CP-specific operator labels (e.g. a role label) so the control-plane pool
	// claims only the designated CP host. Combined with a deterministic CP
	// endpoint (the CP host's IP), this avoids the non-deterministic claim
	// order problem: only the role-tagged host matches the CP selector.
	ControlPlaneHostSelectorMatchLabels map[string]string
}

// ValuesFile returns the Helm values file for BYOT testing.
func (b ByotInfra) ValuesFile() string { return byotValuesFile }

// Overrides returns the Helm value overrides for BYOT testing as flat dotted
// keys (the shape setNestedValue/SetNestedField expects; values must be
// JSON-deep-copyable, so ints are int64). The chart derives a hostSelector from
// resources/os.disk (the prod SKU path) and claims Available ByotHosts
// matching it; the optional operator hostSelector matchLabels scope the claim
// to this test's hosts. Replicas are 1 each for the single CP + single worker
// test pair.
func (b ByotInfra) Overrides() map[string]any {
	overrides := map[string]any{
		"kommodity.provider.config.controlPlaneEndpoint.host": b.ControlPlaneEndpointHost,
		"kommodity.provider.config.controlPlaneEndpoint.port": int64(b.ControlPlaneEndpointPort),

		"kommodity.controlplane.replicas":         int64(1),
		"kommodity.controlplane.resources.cpu":    b.CPU,
		"kommodity.controlplane.resources.memory": b.Memory,
		"kommodity.controlplane.os.disk.type":     b.DiskType,
		"kommodity.controlplane.os.disk.size":     b.DiskSize,
		"kommodity.controlplane.strategicPatches": talosInDockerPatch(),

		"kommodity.nodepools.default.replicas":         int64(1),
		"kommodity.nodepools.default.resources.cpu":    b.CPU,
		"kommodity.nodepools.default.resources.memory": b.Memory,
		"kommodity.nodepools.default.os.disk.type":     b.DiskType,
		"kommodity.nodepools.default.os.disk.size":     b.DiskSize,
		"kommodity.nodepools.default.strategicPatches": talosInDockerPatch(),
		// Unset the per-FD zones from values.byot.yaml so the test runs a single
		// domain-less worker MachineDeployment (one worker, not split across FDs).
		"kommodity.nodepools.default.zones": nil,
	}

	cpSelector := mergeMatchLabels(b.HostSelectorMatchLabels, b.ControlPlaneHostSelectorMatchLabels)
	if len(cpSelector) > 0 {
		overrides["kommodity.controlplane.hostSelector.matchLabels"] = toStringAnyMap(cpSelector)
	}

	if len(b.HostSelectorMatchLabels) > 0 {
		overrides["kommodity.nodepools.default.hostSelector.matchLabels"] = toStringAnyMap(b.HostSelectorMatchLabels)
	}

	return overrides
}

// mergeMatchLabels combines a base operator selector with an override map,
// override winning on key conflict. Returns a new map (nil when both empty).
func mergeMatchLabels(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	maps.Copy(out, override)

	return out
}

// toStringAnyMap converts a map[string]string to map[string]any. Helm values
// are JSON-marshalled and deep-copied via k8s DeepCopyJSONValue, which only
// handles map[string]any (not map[string]string), so label maps must be
// converted before being set as nested values.
func toStringAnyMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// talosInDockerPatch returns the machine config patch Talos-in-Docker needs
// per https://docs.siderolabs.com/talos/v1.13/platform-specific-installations/local-platforms/docker:
// host DNS forwarding, since a container cannot provide its own resolvers.
// The chart deep-merges strategicPatches (a partial MachineConfig dict) into
// the base config.
func talosInDockerPatch() map[string]any {
	return map[string]any{
		"machine": map[string]any{
			"features": map[string]any{
				"hostDNS": map[string]any{
					"enabled":              true,
					"forwardKubeDNSToHost": true,
				},
			},
		},
	}
}

// TalosNode describes a running Talos-in-Docker node under test.
type TalosNode struct {
	Container    tc.Container
	InternalIP   string
	TalosAPIAddr string // host-mapped Talos gRPC endpoint for test-side probes
	APIServerURL string
}

// StartTalosNodes boots the CP and worker maintenance-mode Talos containers and
// returns their runtime info (internal IP used by the kommodity container on the
// shared docker network, host-mapped kube-apiserver URL for test assertions).
func StartTalosNodes(
	t *testing.T,
	env TestEnvironment,
	testName string,
	talosVersion string,
) (TalosNode, TalosNode) {
	t.Helper()

	ctx := context.Background()

	controlPlane := startTalosContainer(ctx, t, env, testName+"-cp", talosVersion)
	worker := startTalosContainer(ctx, t, env, testName+"-worker", talosVersion)

	controlPlaneInfo := talosNodeInfo(ctx, t, controlPlane, env)
	workerInfo := talosNodeInfo(ctx, t, worker, env)

	return controlPlaneInfo, workerInfo
}

// startTalosContainer starts a single privileged Talos container on the shared network.
func startTalosContainer(
	ctx context.Context,
	t *testing.T,
	env TestEnvironment,
	name string,
	talosVersion string,
) tc.Container {
	t.Helper()

	image := byotTalosImageRepo + ":" + talosVersion

	container, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:      image,
			Name:       name,
			Hostname:   name,
			Privileged: true,
			Networks:   []string{env.Network.Name},
			NetworkAliases: map[string][]string{
				env.Network.Name: {name},
			},
			Env: map[string]string{
				"PLATFORM": "container",
			},
			ExposedPorts: []string{
				byotTalosAPIPort,
				byotKubeAPIPort,
			},
			Mounts: tc.ContainerMounts{
				{Source: tc.DockerVolumeMountSource{}, Target: "/system/state"},
				{Source: tc.DockerVolumeMountSource{}, Target: "/var"},
				{Source: tc.DockerVolumeMountSource{}, Target: "/etc/cni"},
				{Source: tc.DockerVolumeMountSource{}, Target: "/etc/kubernetes"},
				{Source: tc.DockerVolumeMountSource{}, Target: "/usr/libexec/kubernetes"},
				{Source: tc.DockerVolumeMountSource{}, Target: "/opt"},
			},
			HostConfigModifier: func(hostConfig *dockercontainer.HostConfig) {
				hostConfig.Privileged = true
				hostConfig.SecurityOpt = []string{"seccomp=unconfined"}
				hostConfig.Tmpfs = map[string]string{
					"/run":    "",
					"/system": "",
					"/tmp":    "",
				}
				hostConfig.RestartPolicy = dockercontainer.RestartPolicy{
					Name: dockercontainer.RestartPolicyAlways,
				}
			},
			WaitingFor: wait.ForListeningPort(byotTalosAPIPort).
				WithStartupTimeout(talosStartupTimeout),
		},
		Started: true,
	})
	require.NoError(t, err)

	return container
}

// talosNodeInfo inspects a container and builds its TalosNode descriptor.
func talosNodeInfo(ctx context.Context, t *testing.T, container tc.Container, env TestEnvironment) TalosNode {
	t.Helper()

	inspect, err := container.Inspect(ctx)
	require.NoError(t, err)

	internalIP := ""
	if netw, ok := inspect.NetworkSettings.Networks[env.Network.Name]; ok {
		internalIP = netw.IPAddress
	}

	require.NotEmpty(t, internalIP, "talos container must have an IP on the shared network")

	host, err := container.Host(ctx)
	require.NoError(t, err)

	if host == "localhost" {
		// Rootless podman only binds published ports on IPv4; "localhost" may
		// resolve to ::1 and refuse.
		host = "127.0.0.1"
	}

	kubePort, err := container.MappedPort(ctx, byotKubeAPIPort)
	require.NoError(t, err)

	talosPort, err := container.MappedPort(ctx, byotTalosAPIPort)
	require.NoError(t, err)

	return TalosNode{
		Container:    container,
		InternalIP:   internalIP,
		TalosAPIAddr: net.JoinHostPort(host, talosPort.Port()),
		APIServerURL: "https://" + net.JoinHostPort(host, kubePort.Port()),
	}
}

// TerminateTalosNodes removes Talos test containers.
func TerminateTalosNodes(t *testing.T, nodes ...TalosNode) {
	t.Helper()

	ctx := context.Background()

	for _, node := range nodes {
		err := node.Container.Terminate(ctx, tc.RemoveVolumes())
		require.NoError(t, err)
	}
}

// GetTalosVersion reads the talos.version from the BYOT example values file.
func GetTalosVersion(t *testing.T) string {
	t.Helper()

	repoRoot, err := FindRepoRoot()
	require.NoError(t, err)

	valuesPath := filepath.Join(repoRoot, "charts", "kommodity-cluster", byotValuesFile)

	values, err := chartutil.ReadValuesFile(valuesPath)
	require.NoError(t, err)

	version, err := getNestedString(values, "talos.version")
	require.NoError(t, err)

	return version
}

// ProbeMaintenance reports whether the node answers the maintenance-mode Talos
// API without authentication. The gRPC endpoint is talosAPIAddr (host:port,
// host-mapped); node must be the machine identity Talos put in its apid cert
// (its internal docker IP), not 127.0.0.1.
func ProbeMaintenance(ctx context.Context, talosAPIAddr string, node string) bool {
	//nolint:gosec // Maintenance mode has no PKI material; probe must skip verification.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	client, err := talosclient.New(ctx, talosclient.WithTLSConfig(tlsConfig), talosclient.WithEndpoints(talosAPIAddr))
	if err != nil {
		return false
	}

	defer client.Close() //nolint:errcheck

	probeCtx, cancel := context.WithTimeout(talosclient.WithNode(ctx, node), talosProbeTimeout)
	defer cancel()

	_, err = client.Version(probeCtx)

	return err == nil
}

// ProbeAuthenticated reports whether the node answers the Talos machine API
// with credentials from the given talosconfig bytes. See ProbeMaintenance for
// the node/endpoint distinction.
func ProbeAuthenticated(ctx context.Context, talosAPIAddr string, node string, talosConfig []byte) bool {
	config, err := clientconfig.FromBytes(talosConfig)
	if err != nil {
		return false
	}

	client, err := talosclient.New(
		ctx,
		talosclient.WithConfig(config),
		talosclient.WithEndpoints(talosAPIAddr),
	)
	if err != nil {
		return false
	}

	defer client.Close() //nolint:errcheck

	probeCtx, cancel := context.WithTimeout(talosclient.WithNode(ctx, node), talosProbeTimeout)
	defer cancel()

	_, err = client.Version(probeCtx)

	return err == nil
}

// GetWorkloadClient builds a kubernetes client for the workload cluster using
// the CAPI-generated kubeconfig, with the API server endpoint rewritten to the
// host-mapped port of the control plane Talos container.
func GetWorkloadClient(
	t *testing.T,
	env TestEnvironment,
	clusterName string,
	namespace string,
	overrideHost string,
) *kubernetes.Clientset {
	t.Helper()

	kommodityClient, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	secret, err := kommodityClient.CoreV1().Secrets(namespace).Get(
		context.Background(),
		clusterName+"-kubeconfig",
		metav1.GetOptions{},
	)
	require.NoError(t, err)

	kubeconfig, ok := secret.Data["value"]
	require.True(t, ok, "kubeconfig secret must contain a 'value' key")

	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
	require.NoError(t, err)

	workloadCfg, err := clientConfig.ClientConfig()
	require.NoError(t, err)

	workloadCfg.Host = overrideHost

	client, err := kubernetes.NewForConfig(workloadCfg)
	require.NoError(t, err)

	return client
}

// GetClusterTalosconfig fetches the talosconfig secret payload CAPI generated
// for the given cluster (secret <cluster>-talosconfig, key "talosconfig").
func GetClusterTalosconfig(t *testing.T, env TestEnvironment, clusterName string, namespace string) []byte {
	t.Helper()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets(namespace).Get(
		context.Background(),
		clusterName+"-talosconfig",
		metav1.GetOptions{},
	)
	require.NoError(t, err)

	talosconfig, ok := secret.Data["talosconfig"]
	require.True(t, ok, "talosconfig secret must contain a 'talosconfig' key")

	return talosconfig
}

// CreateTalosconfigSecret seeds kommodity with the talosconfig credential a
// ByotMachine's talosConfigSecretRef points at.
func CreateTalosconfigSecret(
	t *testing.T,
	env TestEnvironment,
	secretName string,
	namespace string,
	talosconfig []byte,
) {
	t.Helper()

	client, err := kubernetes.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	_, err = client.CoreV1().Secrets(namespace).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"talosconfig": talosconfig,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

// ByotMachine GVR coordinates.
const (
	byotMachineGroup    = "infrastructure.cluster.x-k8s.io"
	byotMachineVersion  = "v1alpha1"
	byotMachineResource = "byotmachines"
)

func byotMachineGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    byotMachineGroup,
		Version:  byotMachineVersion,
		Resource: byotMachineResource,
	}
}

// ByotMachineExistsForCluster reports whether a ByotMachine with the given
// name exists and carries the cluster.x-k8s.io/cluster-name label matching
// clusterName. Used to assert a host's claimRef names a machine of the right
// cluster without hard-coding the controller-generated Machine name.
func ByotMachineExistsForCluster(
	t *testing.T,
	env TestEnvironment,
	machineName string,
	namespace string,
	clusterName string,
) bool {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return false
	}

	obj, err := client.Resource(byotMachineGVR()).Namespace(namespace).Get(
		context.Background(), machineName, metav1.GetOptions{})
	if err != nil {
		return false
	}

	label, found, _ := unstructured.NestedString(obj.Object, "metadata", "labels", "cluster.x-k8s.io/cluster-name")

	return found && label == clusterName
}

// ByotMachineConditionState returns the current status+reason of a single
// condition on a ByotMachine.
func ByotMachineConditionState(
	t *testing.T,
	env TestEnvironment,
	namespace string,
	machineName string,
	conditionType string,
) (corev1.ConditionStatus, string) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	obj, err := client.Resource(schema.GroupVersionResource{
		Group:    byotMachineGroup,
		Version:  byotMachineVersion,
		Resource: byotMachineResource,
	}).Namespace(namespace).Get(context.Background(), machineName, metav1.GetOptions{})
	require.NoError(t, err)

	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	require.NoError(t, err)
	require.True(t, found, "ByotMachine %s must have status.conditions", machineName)

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}

		condType, _, _ := unstructured.NestedString(condMap, "type")
		if condType != conditionType {
			continue
		}

		status, _, _ := unstructured.NestedString(condMap, "status")
		reason, _, _ := unstructured.NestedString(condMap, "reason")

		return corev1.ConditionStatus(status), reason
	}

	return corev1.ConditionUnknown, ""
}

// ByotMachineTerminating reports whether a BYOT machine has a non-nil
// deletionTimestamp.
func ByotMachineTerminating(
	ctx context.Context,
	env TestEnvironment,
	namespace string,
	name string,
) bool {
	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return false
	}

	obj, err := client.Resource(byotMachineGVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false
	}

	return obj.GetDeletionTimestamp() != nil
}

// CleanupTerminatingByotMachines strips finalizers from terminating BYOT
// machines so stuck deletes complete (used where a wipe cannot succeed, e.g.
// on docker nodes without block devices).
func CleanupTerminatingByotMachines(
	ctx context.Context,
	env TestEnvironment,
	namespace string,
	names ...string,
) {
	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return
	}

	for _, name := range names {
		obj, err := client.Resource(byotMachineGVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue
		}

		obj.SetFinalizers(nil)

		_, err = client.Resource(byotMachineGVR()).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			log.Printf("failed to strip finalizers on %s: %v", name, err)
		}
	}
}

// DumpByotMachines logs every ByotMachine in the namespace with status
// conditions, for post-mortem diagnosis. Never fails the test.
func DumpByotMachines(ctx context.Context, env TestEnvironment, namespace string) {
	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return
	}

	list, err := client.Resource(schema.GroupVersionResource{
		Group:    byotMachineGroup,
		Version:  byotMachineVersion,
		Resource: byotMachineResource,
	}).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	for _, item := range list.Items {
		conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")

		parts := []string{}

		for _, cond := range conditions {
			condMap, ok := cond.(map[string]any)
			if !ok {
				continue
			}

			condType, _, _ := unstructured.NestedString(condMap, "type")
			condStatus, _, _ := unstructured.NestedString(condMap, "status")
			reason, _, _ := unstructured.NestedString(condMap, "reason")
			message, _, _ := unstructured.NestedString(condMap, "message")

			parts = append(parts,
				fmt.Sprintf("%s=%s/%s msg=%q", condType, condStatus, reason, message))
		}

		ready, _, _ := unstructured.NestedBool(item.Object, "status", "ready")
		log.Printf(
			"byotMachine %s ready=%v conds=%s",
			item.GetName(), ready, strings.Join(parts, " | "),
		)
	}
}

// WaitForByotMachineDeletion polls until the named ByotMachine is gone from
// the cluster. CAPI deletes a ByotMachine only after its finalizer is
// removed, which lags behind the owning Machine's deletion; callers that
// re-create the ByotMachine via a helm upgrade must wait for this first,
// otherwise helm sees the still-terminating object and skips re-creating it.
func WaitForByotMachineDeletion(
	t *testing.T,
	env TestEnvironment,
	namespace string,
	machineName string,
	timeout time.Duration,
) {
	t.Helper()

	require.NoError(t, WaitForK8sResourceDeletion(
		env.KommodityCfg, namespace, machineName,
		byotMachineGroup, byotMachineVersion, byotMachineResource, "", "", timeout))
}

// ClusterByotMachineNames returns the names of all ByotMachines carrying the
// cluster.x-k8s.io/cluster-name label for clusterName. Used to strip finalizers
// on hosts where the release reset cannot complete (docker).
func ClusterByotMachineNames(t *testing.T, env TestEnvironment, clusterName string, namespace string) []string {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	list, err := client.Resource(byotMachineGVR()).Namespace(namespace).List(
		context.Background(), metav1.ListOptions{
			LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
		})
	require.NoError(t, err)

	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}

	return names
}

// ClusterByotMachinesTerminating reports whether every ByotMachine of the
// given cluster has a non-nil deletionTimestamp (the finalizer is running).
// Returns true when there are no machines left (already deleted) or all are
// terminating; false when at least one is still live.
func ClusterByotMachinesTerminating(t *testing.T, env TestEnvironment, clusterName string, namespace string) bool {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return false
	}

	list, err := client.Resource(byotMachineGVR()).Namespace(namespace).List(
		context.Background(), metav1.ListOptions{
			LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
		})
	if err != nil {
		return false
	}

	if len(list.Items) == 0 {
		return true // all gone
	}

	for _, item := range list.Items {
		if item.GetDeletionTimestamp() == nil {
			return false // at least one not yet terminating
		}
	}

	return true
}

// WaitForClusterByotMachinesDeletion polls until no ByotMachines carrying the
// cluster.x-k8s.io/cluster-name label for clusterName remain. CAPI
// cascade-deletes Machines → ByotMachines on cluster uninstall; this confirms
// the machine layer is gone without knowing the controller-generated names.
func WaitForClusterByotMachinesDeletion(
	t *testing.T,
	env TestEnvironment,
	clusterName string,
	namespace string,
	timeout time.Duration,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	check := func(ctx context.Context) (bool, error) {
		client, err := dynamic.NewForConfig(env.KommodityCfg)
		if err != nil {
			return false, nil //nolint:nilerr // keep polling
		}

		list, err := client.Resource(byotMachineGVR()).Namespace(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
		})
		if err != nil {
			return false, nil //nolint:nilerr // keep polling
		}

		return len(list.Items) == 0, nil
	}

	err := waitPoll(ctx, byotPollInterval, check)
	require.NoError(t, err,
		"ByotMachines for cluster %s were not deleted within %s", clusterName, timeout)
}

// WaitForByotMachineCondition polls a ByotMachine until the given status
// condition reports the wanted status, then returns its reason and message.
func WaitForByotMachineCondition(
	t *testing.T,
	env TestEnvironment,
	namespace string,
	machineName string,
	conditionType string,
	wantedStatus corev1.ConditionStatus,
	timeout time.Duration,
) (string, string) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	gvr := schema.GroupVersionResource{
		Group:    byotMachineGroup,
		Version:  byotMachineVersion,
		Resource: byotMachineResource,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	reason := ""
	message := ""

	check := byotMachineConditionCheck(client, gvr, namespace, machineName, conditionType, wantedStatus, &reason, &message)

	errPoll := waitPoll(ctx, byotPollInterval, check)
	require.NoError(t, errPoll,
		"ByotMachine %s condition %s never became %s (last reason %q message %q)",
		machineName, conditionType, wantedStatus, reason, message)

	return reason, message
}

// byotMachineConditionCheck builds the poll closure that reads one ByotMachine
// and reports when the wanted condition status appears.
func byotMachineConditionCheck(
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace string,
	machineName string,
	conditionType string,
	wantedStatus corev1.ConditionStatus,
	reason *string,
	message *string,
) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		obj, err := client.Resource(gvr).Namespace(namespace).Get(
			ctx, machineName, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr // keep polling until timeout or resource appears
		}

		conditions, found, nestedErr := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if nestedErr != nil || !found {
			//nolint:nilerr // keep polling until the CRD conditions appear
			return false, nil
		}

		for _, cond := range conditions {
			condMap, ok := cond.(map[string]any)
			if !ok {
				continue
			}

			condType, _, _ := unstructured.NestedString(condMap, "type")
			if condType != conditionType {
				continue
			}

			status, _, _ := unstructured.NestedString(condMap, "status")
			*reason, _, _ = unstructured.NestedString(condMap, "reason")
			*message, _, _ = unstructured.NestedString(condMap, "message")

			if corev1.ConditionStatus(status) == wantedStatus {
				return true, nil
			}
		}

		return false, nil
	}
}

// waitPoll is a small poll loop since pkg/test has no k8s.io/apimachinery/util/wait wrapper yet.
func waitPoll(ctx context.Context, interval time.Duration, condition func(context.Context) (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		done, err := condition(ctx)
		if err != nil {
			return err
		}

		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for condition: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// ByotHost GVR coordinates.
const (
	byotHostGroup    = "infrastructure.cluster.x-k8s.io"
	byotHostVersion  = "v1alpha1"
	byotHostResource = "byothosts"
)

func byotHostGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    byotHostGroup,
		Version:  byotHostVersion,
		Resource: byotHostResource,
	}
}

// ByotHostPhase values mirror the provider's HostPhase constants.
const (
	ByotHostPhaseProbing   = "Probing"
	ByotHostPhaseAvailable = "Available"
	ByotHostPhaseClaimed   = "Claimed"
	ByotHostPhaseReleasing = "Releasing"
	ByotHostPhaseUnavail   = "Unavailable"
)

// CreateByotHost creates a ByotHost object (an IP-only record of a
// maintenance-mode Talos box) in the kommodity cluster. The ByotHost controller
// (running in the kommodity container) discovers hardware and probes liveness,
// promoting it to Available once the maintenance API answers. labels are set
// as operator (non-byot.io/) metadata labels; the controller preserves them
// and they can be matched by a hostSelector to scope a claim.
func CreateByotHost(
	t *testing.T,
	env TestEnvironment,
	name string,
	namespace string,
	publicIP string,
	labels map[string]string,
) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	host := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": byotHostGroup + "/" + byotHostVersion,
			"kind":       "ByotHost",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"publicIP": publicIP,
			},
		},
	}

	_, err = client.Resource(byotHostGVR()).Namespace(namespace).Create(
		context.Background(), host, metav1.CreateOptions{})
	require.NoError(t, err)
}

// DeleteByotHost deletes a ByotHost object (the controller removes its
// finalizer once the claim is released).
func DeleteByotHost(t *testing.T, env TestEnvironment, name string, namespace string) {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	err = client.Resource(byotHostGVR()).Namespace(namespace).Delete(
		context.Background(), name, metav1.DeleteOptions{})
	require.NoError(t, err)
}

// ByotHostLabels returns the metadata.labels of a ByotHost. The host
// controller promotes curated byot.io/ labels from discovery (cpu-cores,
// memory-class, disk-type, disk-class, available, ...) and preserves operator
// (non-byot.io/) labels; tests read the promoted labels to build a matching
// resources/os.disk selector.
func ByotHostLabels(t *testing.T, env TestEnvironment, name string, namespace string) map[string]string {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	obj, err := client.Resource(byotHostGVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)

	labels, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "labels")

	return labels
}

// ByotHostPhase returns the current status.phase of a ByotHost.
func ByotHostPhase(t *testing.T, env TestEnvironment, name string, namespace string) string {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	obj, err := client.Resource(byotHostGVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)

	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")

	return phase
}

// ByotHostClaimRefName returns the name of the ByotMachine claiming the host,
// or empty when unclaimed.
func ByotHostClaimRefName(t *testing.T, env TestEnvironment, name string, namespace string) string {
	t.Helper()

	client, err := dynamic.NewForConfig(env.KommodityCfg)
	require.NoError(t, err)

	obj, err := client.Resource(byotHostGVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)

	claimName, _, _ := unstructured.NestedString(obj.Object, "status", "claimRef", "name")

	return claimName
}

// WaitForByotHostPhase polls a ByotHost until status.phase matches the wanted
// phase, returning the observed phase.
func WaitForByotHostPhase(
	t *testing.T,
	env TestEnvironment,
	name string,
	namespace string,
	wantedPhase string,
	timeout time.Duration,
) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var phase string

	check := func(ctx context.Context) (bool, error) {
		client, err := dynamic.NewForConfig(env.KommodityCfg)
		if err != nil {
			return false, nil //nolint:nilerr // keep polling
		}

		obj, err := client.Resource(byotHostGVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr // keep polling until the CRD/object appears
		}

		phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")

		return phase == wantedPhase, nil
	}

	err := waitPoll(ctx, byotPollInterval, check)
	require.NoError(t, err,
		"ByotHost %s never reached phase %s (last %q)", name, wantedPhase, phase)

	return phase
}

// WaitForByotHostDeletion polls until the named ByotHost is gone from the
// cluster. A claimed host has a finalizer; delete the owning ByotMachine first
// (which releases + clears claimRef) or the host stays terminating.
func WaitForByotHostDeletion(
	t *testing.T,
	env TestEnvironment,
	name string,
	namespace string,
	timeout time.Duration,
) {
	t.Helper()

	require.NoError(t, WaitForK8sResourceDeletion(
		env.KommodityCfg, namespace, name,
		byotHostGroup, byotHostVersion, byotHostResource, "", "", timeout))
}

// DumpByotHosts logs every ByotHost in the namespace with phase, publicIP and
// claimRef, for post-mortem diagnosis. Never fails the test.
func DumpByotHosts(ctx context.Context, env TestEnvironment, namespace string) {
	client, err := dynamic.NewForConfig(env.KommodityCfg)
	if err != nil {
		return
	}

	list, err := client.Resource(byotHostGVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	for _, item := range list.Items {
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		publicIP, _, _ := unstructured.NestedString(item.Object, "spec", "publicIP")
		claimName, _, _ := unstructured.NestedString(item.Object, "status", "claimRef", "name")

		log.Printf(
			"byotHost %s phase=%s publicIP=%s claimRef=%s",
			item.GetName(), phase, publicIP, claimName,
		)
	}
}
