package helpers

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
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

// ByotInfra carries Helm value overrides for BYOT (Talos-in-Docker) clusters.
type ByotInfra struct {
	ControlPlaneIP       string
	WorkerIP             string
	ControlPlaneName     string
	WorkerName           string
	JoinPolicy           string
	SplitPolicy          string
	TalosConfigRef       string
	WorkerJoinPolicy     string
	WorkerSplitPolicy    string
	WorkerTalosConfigRef string
}

// ValuesFile returns the Helm values file for BYOT testing.
func (b ByotInfra) ValuesFile() string { return byotValuesFile }

// Overrides returns the Helm value overrides for BYOT testing.
func (b ByotInfra) Overrides() map[string]any {
	cpMachine := map[string]any{
		"publicIP":         b.ControlPlaneIP,
		"init":             true,
		"strategicPatches": talosInDockerPatches(),
	}

	addPolicyOverrides(cpMachine, b.JoinPolicy, b.SplitPolicy, b.TalosConfigRef)

	workerMachine := map[string]any{
		"publicIP":         b.WorkerIP,
		"strategicPatches": talosInDockerPatches(),
	}

	addPolicyOverrides(workerMachine, b.WorkerJoinPolicy, b.WorkerSplitPolicy, b.WorkerTalosConfigRef)

	return map[string]any{
		"kommodity.controlplane.staticMachines": map[string]any{
			b.ControlPlaneName: cpMachine,
		},
		"kommodity.nodepools.default.staticMachines": map[string]any{
			b.WorkerName: workerMachine,
		},
		"kommodity.init": false,
	}
}

// talosInDockerPatches returns the machine config patches Talos-in-Docker needs
// per https://docs.siderolabs.com/talos/v1.13/platform-specific-installations/local-platforms/docker:
// host DNS forwarding, since a container cannot provide its own resolvers.
// The chart renders strategicPatches as raw YAML strings.
func talosInDockerPatches() []any {
	return []any{
		`machine:
  features:
    hostDNS:
      enabled: true
      forwardKubeDNSToHost: true
`,
	}
}

// addPolicyOverrides injects join/split policy fields into a static machine map.
func addPolicyOverrides(machine map[string]any, joinPolicy string, splitPolicy string, talosConfigRef string) {
	if joinPolicy != "" {
		machine["joinPolicy"] = joinPolicy
	}

	if splitPolicy != "" {
		machine["splitPolicy"] = splitPolicy
	}

	if talosConfigRef != "" {
		machine["talosConfigSecretRef"] = map[string]any{
			"name": talosConfigRef,
		}
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
