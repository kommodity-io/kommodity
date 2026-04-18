package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	clientgoclientset "k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	taloscontrolplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	clusterNameLabel = "cluster.x-k8s.io/cluster-name"
	defaultNamespace = "default"
)

// DashboardMetrics holds the metrics for the dashboard.
type DashboardMetrics struct {
	Clusters     int `json:"clusters"`
	Running      int `json:"running"`
	Provisioning int `json:"provisioning"`
	Total        int `json:"total"`
}

// ClusterInfo holds information about a cluster.
type ClusterInfo struct {
	Name              string `json:"name"`
	ChartVersion      string `json:"chartVersion"`
	KubernetesVersion string `json:"kubernetesVersion"`
	TalosVersion      string `json:"talosVersion"`
}

// GetDashboardMetrics retrieves the dashboard metrics.
func GetDashboardMetrics(
	ctx context.Context,
	client ctrlclient.Client,
) (*DashboardMetrics, error) {
	logger := logging.FromContext(ctx)

	// Get all clusters
	var clusters clusterv1.ClusterList

	err := client.List(ctx, &clusters)
	if err != nil {
		logger.Error("Failed to list clusters", zap.Error(err))

		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	// Get all machines
	var machines clusterv1.MachineList

	err = client.List(ctx, &machines)
	if err != nil {
		logger.Error("Failed to list machines", zap.Error(err))

		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	// Count machines by phase
	running := 0
	provisioning := 0

	for _, machine := range machines.Items {
		switch machine.Status.Phase {
		case "Running":
			running++
		case "Provisioning":
			provisioning++
		}
	}

	metrics := &DashboardMetrics{
		Clusters:     len(clusters.Items),
		Running:      running,
		Provisioning: provisioning,
		Total:        len(machines.Items),
	}

	logger.Debug("Retrieved dashboard metrics",
		zap.Int("clusters", metrics.Clusters),
		zap.Int("running", metrics.Running),
		zap.Int("provisioning", metrics.Provisioning),
		zap.Int("total", metrics.Total),
	)

	return metrics, nil
}

// GetClusterList retrieves the list of clusters with their versions.
func GetClusterList(
	ctx context.Context,
	client ctrlclient.Client,
	kubeClient *clientgoclientset.Clientset,
) ([]ClusterInfo, error) {
	logger := logging.FromContext(ctx)

	// Get all clusters
	var clusters clusterv1.ClusterList

	err := client.List(ctx, &clusters)
	if err != nil {
		logger.Error("Failed to list clusters", zap.Error(err))

		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	clusterInfos := make([]ClusterInfo, 0, len(clusters.Items))

	for _, cluster := range clusters.Items {
		info := buildClusterInfo(ctx, client, kubeClient, cluster)
		clusterInfos = append(clusterInfos, info)
	}

	logger.Debug("Retrieved cluster list", zap.Int("count", len(clusterInfos)))

	return clusterInfos, nil
}

// buildClusterInfo builds a ClusterInfo struct for a given cluster.
func buildClusterInfo(
	ctx context.Context,
	client ctrlclient.Client,
	kubeClient *clientgoclientset.Clientset,
	cluster clusterv1.Cluster,
) ClusterInfo {
	logger := logging.FromContext(ctx)

	info := ClusterInfo{
		Name:              cluster.Name,
		ChartVersion:      UnknownVersion,
		KubernetesVersion: UnknownVersion,
		TalosVersion:      UnknownVersion,
	}

	// Get chart version from Helm release
	chartVersion, err := getHelmChartVersion(ctx, cluster.Name, cluster.Namespace, kubeClient)
	if err != nil {
		logger.Warn("Failed to get chart version for cluster",
			zap.String("cluster", cluster.Name),
			zap.Error(err),
		)
	} else {
		info.ChartVersion = chartVersion
	}

	// Get Kubernetes version from control plane
	k8sVersion, err := getKubernetesVersion(ctx, client, cluster.Name, cluster.Namespace)
	if err != nil {
		logger.Warn("Failed to get Kubernetes version for cluster",
			zap.String("cluster", cluster.Name),
			zap.Error(err),
		)
	} else {
		info.KubernetesVersion = k8sVersion
	}

	// Get Talos version from TalosControlPlane
	talosVersion, err := getTalosVersionForCluster(ctx, client, cluster.Name, cluster.Namespace)
	if err != nil {
		logger.Warn("Failed to get Talos version for cluster",
			zap.String("cluster", cluster.Name),
			zap.String("namespace", cluster.Namespace),
			zap.Error(err),
		)
	} else {
		info.TalosVersion = talosVersion
	}

	return info
}

// getKubernetesVersion retrieves the Kubernetes version for a cluster.
func getKubernetesVersion(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	namespace string,
) (string, error) {
	// Try to get from TalosControlPlane first
	version, err := getKubernetesVersionFromControlPlane(ctx, client, clusterName, namespace)
	if err == nil && version != "" {
		return version, nil
	}

	// Fallback: try to get from any machine in the cluster
	version, err = getKubernetesVersionFromMachines(ctx, client, clusterName, namespace)
	if err != nil {
		return "", err
	}

	if version != "" {
		return version, nil
	}

	return UnknownVersion, nil
}

// getKubernetesVersionFromControlPlane retrieves K8s version from TalosControlPlane.
func getKubernetesVersionFromControlPlane(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	namespace string,
) (string, error) {
	logger := logging.FromContext(ctx)

	var controlPlaneList taloscontrolplanev1.TalosControlPlaneList

	err := client.List(ctx, &controlPlaneList, &ctrlclient.ListOptions{
		Namespace: namespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list control planes: %w", err)
	}

	logger.Debug("Searching for TalosControlPlane",
		zap.String("clusterName", clusterName),
		zap.String("namespace", namespace),
		zap.Int("totalControlPlanes", len(controlPlaneList.Items)),
	)

	for _, controlPlane := range controlPlaneList.Items {
		logger.Debug("Checking TalosControlPlane",
			zap.String("name", controlPlane.Name),
			zap.Any("labels", controlPlane.Labels),
			zap.String("version", controlPlane.Spec.Version),
		)

		if controlPlane.Labels != nil && controlPlane.Labels[clusterNameLabel] == clusterName {
			if controlPlane.Spec.Version != "" {
				logger.Debug("Found Kubernetes version",
					zap.String("clusterName", clusterName),
					zap.String("version", controlPlane.Spec.Version),
				)

				return controlPlane.Spec.Version, nil
			}
		}
	}

	return "", nil
}

// getKubernetesVersionFromMachines retrieves K8s version from machines.
func getKubernetesVersionFromMachines(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	namespace string,
) (string, error) {
	var machines clusterv1.MachineList

	err := client.List(ctx, &machines, &ctrlclient.ListOptions{
		Namespace: namespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list machines: %w", err)
	}

	for _, machine := range machines.Items {
		if machine.Labels != nil && machine.Labels[clusterNameLabel] == clusterName {
			if machine.Spec.Version != nil && *machine.Spec.Version != "" {
				return *machine.Spec.Version, nil
			}
		}
	}

	return "", nil
}

// getTalosVersionForCluster retrieves the Talos version for a cluster from TalosControlPlane.
func getTalosVersionForCluster(
	ctx context.Context,
	client ctrlclient.Client,
	clusterName string,
	namespace string,
) (string, error) {
	logger := logging.FromContext(ctx)

	// Get TalosControlPlane for this cluster
	var controlPlaneList taloscontrolplanev1.TalosControlPlaneList

	err := client.List(ctx, &controlPlaneList, &ctrlclient.ListOptions{
		Namespace: namespace,
	})
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to list control planes: %w", err)
	}

	logger.Debug("Searching for TalosControlPlane for Talos version",
		zap.String("clusterName", clusterName),
		zap.String("namespace", namespace),
		zap.Int("totalControlPlanes", len(controlPlaneList.Items)),
	)

	// Find control plane for this cluster
	for _, controlPlane := range controlPlaneList.Items {
		logger.Debug("Checking TalosControlPlane for Talos version",
			zap.String("name", controlPlane.Name),
			zap.Any("labels", controlPlane.Labels),
			zap.String("talosVersion", controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion),
		)

		if controlPlane.Labels != nil && controlPlane.Labels[clusterNameLabel] == clusterName {
			// Get Talos version from spec.controlPlaneConfig.controlplane.talosVersion
			if controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion != "" {
				logger.Debug("Found Talos version",
					zap.String("clusterName", clusterName),
					zap.String("version", controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion),
				)

				return controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion, nil
			}
		}
	}

	logger.Warn("No Talos version found for cluster",
		zap.String("clusterName", clusterName),
		zap.String("namespace", namespace),
	)

	return UnknownVersion, nil
}

// helmRelease represents a subset of Helm release data.
type helmRelease struct {
	Chart struct {
		Metadata struct {
			Version string `json:"version"`
		} `json:"metadata"`
	} `json:"chart"`
}

// getHelmChartVersion retrieves the Helm chart version from the release secret.
func getHelmChartVersion(
	ctx context.Context,
	releaseName string,
	namespace string,
	kubeClient *clientgoclientset.Clientset,
) (string, error) {
	// List secrets with Helm release labels
	secrets, err := kubeClient.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("owner=helm,name=%s,status=deployed", releaseName),
	})
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to list helm secrets: %w", err)
	}

	if len(secrets.Items) == 0 {
		return UnknownVersion, fmt.Errorf("%w for %s", ErrNoHelmReleaseSecret, releaseName)
	}

	// Get the release data from the secret
	secret := secrets.Items[0]

	releaseData, ok := secret.Data["release"]
	if !ok {
		return UnknownVersion, ErrReleaseDataNotFound
	}

	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(string(releaseData))
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to decode release data: %w", err)
	}

	// Decompress gzip
	gzipReader, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to create gzip reader: %w", err)
	}

	defer func() {
		closeErr := gzipReader.Close()
		if closeErr != nil {
			err = fmt.Errorf("failed to close gzip reader: %w", closeErr)
		}
	}()

	decompressed, err := io.ReadAll(gzipReader)
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to decompress release data: %w", err)
	}

	// Parse JSON
	var release helmRelease

	err = json.Unmarshal(decompressed, &release)
	if err != nil {
		return UnknownVersion, fmt.Errorf("failed to unmarshal release data: %w", err)
	}

	if release.Chart.Metadata.Version == "" {
		return UnknownVersion, nil
	}

	return release.Chart.Metadata.Version, nil
}

// GetKubeconfig retrieves the kubeconfig for Kommodity.
func GetKubeconfig(_ context.Context) (string, error) {
	// This will be fetched via API endpoint /api/kubeconfig/kommodity
	// The actual implementation is in kubeconfig.go
	// This is just a placeholder
	return "", nil
}
