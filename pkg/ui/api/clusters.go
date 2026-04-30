package api

// ChartInfo represents the chart information for a cluster.
type ChartInfo struct {
	Version string `json:"version"`
}

// KubernetesInfo represents the Kubernetes information for a cluster.
type KubernetesInfo struct {
	Version string `json:"version"`
}

// TalosInfo represents the Talos information for a cluster.
type TalosInfo struct {
	Version string `json:"version"`
}

// ClusterAPIItem represents a single cluster in the API response.
type ClusterAPIItem struct {
	Name       string         `json:"name"`
	Chart      ChartInfo      `json:"chart"`
	Kubernetes KubernetesInfo `json:"kubernetes"`
	Talos      TalosInfo      `json:"talos"`
}

// ClusterAPIResponse represents the response for the clusters API endpoint.
type ClusterAPIResponse struct {
	Clusters []ClusterAPIItem `json:"clusters"`
}

// TransformClusterInfoToAPI converts a slice of ClusterInfo to ClusterAPIResponse.
func TransformClusterInfoToAPI(clusterInfos []ClusterInfo) ClusterAPIResponse {
	items := make([]ClusterAPIItem, 0, len(clusterInfos))

	for _, info := range clusterInfos {
		items = append(items, ClusterAPIItem{
			Name: info.Name,
			Chart: ChartInfo{
				Version: info.ChartVersion,
			},
			Kubernetes: KubernetesInfo{
				Version: info.KubernetesVersion,
			},
			Talos: TalosInfo{
				Version: info.TalosVersion,
			},
		})
	}

	return ClusterAPIResponse{
		Clusters: items,
	}
}
