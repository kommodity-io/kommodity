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

// helmRelease represents a minimal subset of Helm release data needed to extract chart version.
type helmRelease struct {
	Chart *struct {
		Metadata *struct {
			Version string `json:"version"`
		} `json:"metadata"`
	} `json:"chart"`
}


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
		case MachinePhaseRunning:
			running++
		case MachinePhaseProvisioning:
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

	// Batch fetch all resources to avoid N+1 queries
	controlPlanesMap, err := fetchAllControlPlanes(ctx, client)
	if err != nil {
		logger.Warn("Failed to fetch control planes", zap.Error(err))
		// Continue with empty map
		controlPlanesMap = make(map[string]*taloscontrolplanev1.TalosControlPlane)
	}

	helmSecretsMap, err := fetchAllHelmSecrets(ctx, kubeClient)
	if err != nil {
		logger.Warn("Failed to fetch helm secrets", zap.Error(err))
		// Continue with empty map
		helmSecretsMap = make(map[string]string)
	}

	clusterInfos := make([]ClusterInfo, 0, len(clusters.Items))

	for _, cluster := range clusters.Items {
		info := buildClusterInfoFromCache(ctx, cluster, controlPlanesMap, helmSecretsMap)
		clusterInfos = append(clusterInfos, info)
	}

	logger.Debug("Retrieved cluster list", zap.Int("count", len(clusterInfos)))

	return clusterInfos, nil
}

// fetchAllControlPlanes fetches all TalosControlPlanes and returns them as a map keyed by cluster name.
func fetchAllControlPlanes(
	ctx context.Context,
	client ctrlclient.Client,
) (map[string]*taloscontrolplanev1.TalosControlPlane, error) {
	var controlPlaneList taloscontrolplanev1.TalosControlPlaneList

	err := client.List(ctx, &controlPlaneList)
	if err != nil {
		return nil, fmt.Errorf("failed to list control planes: %w", err)
	}

	controlPlanesMap := make(map[string]*taloscontrolplanev1.TalosControlPlane, len(controlPlaneList.Items))

	for i := range controlPlaneList.Items {
		cp := &controlPlaneList.Items[i]
		if cp.Labels != nil {
			if clusterName, ok := cp.Labels[ClusterNameLabel]; ok {
				controlPlanesMap[clusterName] = cp
			}
		}
	}

	return controlPlanesMap, nil
}

// fetchAllHelmSecrets fetches all Helm release secrets and returns them as a map keyed by release name.
func fetchAllHelmSecrets(
	ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
) (map[string]string, error) {
	logger := logging.FromContext(ctx)

	// List all Helm release secrets across all namespaces
	secrets, err := kubeClient.CoreV1().Secrets("").List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
			HelmLabelOwner, HelmOwnerHelm,
			HelmLabelStatus, HelmStatusDeployed),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list helm secrets: %w", err)
	}

	helmSecretsMap := make(map[string]string, len(secrets.Items))

	for _, secret := range secrets.Items {
		releaseName := secret.Labels[HelmLabelName]
		if releaseName == "" {
			continue
		}

		releaseData, ok := secret.Data[HelmSecretKeyRelease]
		if !ok {
			continue
		}

		version, err := decodeHelmRelease(releaseData)
		if err != nil {
			logger.Warn("Failed to decode helm release",
				zap.String("releaseName", releaseName),
				zap.Error(err),
			)

			continue
		}

		helmSecretsMap[releaseName] = version
	}

	return helmSecretsMap, nil
}

// decodeHelmRelease decodes a Helm release secret and extracts the chart version.
func decodeHelmRelease(releaseData []byte) (string, error) {
	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(string(releaseData))
	if err != nil {
		return "", fmt.Errorf("failed to decode release data: %w", err)
	}

	// Decompress gzip
	gzipReader, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}

	defer func() {
		_ = gzipReader.Close()
	}()

	decompressed, err := io.ReadAll(gzipReader)
	if err != nil {
		return "", fmt.Errorf("failed to decompress release data: %w", err)
	}

	// Decode JSON (Helm v3 uses JSON encoding)
	var rel helmRelease

	err = json.Unmarshal(decompressed, &rel)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal helm release: %w", err)
	}

	if rel.Chart == nil || rel.Chart.Metadata == nil || rel.Chart.Metadata.Version == "" {
		return "", ErrChartVersionNotFound
	}

	return rel.Chart.Metadata.Version, nil
}

// buildClusterInfoFromCache builds a ClusterInfo struct using pre-fetched data.
func buildClusterInfoFromCache(
	ctx context.Context,
	cluster clusterv1.Cluster,
	controlPlanesMap map[string]*taloscontrolplanev1.TalosControlPlane,
	helmSecretsMap map[string]string,
) ClusterInfo {
	logger := logging.FromContext(ctx)

	info := ClusterInfo{
		Name:              cluster.Name,
		ChartVersion:      UnknownVersion,
		KubernetesVersion: UnknownVersion,
		TalosVersion:      UnknownVersion,
	}

	// Get chart version from pre-fetched Helm secrets
	if chartVersion, ok := helmSecretsMap[cluster.Name]; ok {
		info.ChartVersion = chartVersion
	} else {
		logger.Debug("No Helm chart version found for cluster",
			zap.String("cluster", cluster.Name),
		)
	}

	// Get Kubernetes and Talos versions from pre-fetched control plane
	if controlPlane, ok := controlPlanesMap[cluster.Name]; ok {
		if controlPlane.Spec.Version != "" {
			info.KubernetesVersion = controlPlane.Spec.Version
		}

		if controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion != "" {
			info.TalosVersion = controlPlane.Spec.ControlPlaneConfig.ControlPlaneConfig.TalosVersion
		}
	} else {
		logger.Debug("No control plane found for cluster",
			zap.String("cluster", cluster.Name),
		)
	}

	return info
}


