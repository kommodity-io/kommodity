package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"

	taloscontrolplanev1 "github.com/siderolabs/cluster-api-control-plane-provider-talos/api/v1alpha3"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	componentbaseversion "k8s.io/component-base/version"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/ui/api"
)

// KubeconfigSection holds configuration for rendering a kubeconfig section.
type KubeconfigSection struct {
	ID      string
	Content string
}

// MetricCard holds data for a single metric card.
type MetricCard struct {
	Value int
	Label string
}

// buildDashboardMetricCards converts DashboardMetrics into a slice of MetricCards.
func buildDashboardMetricCards(metrics *api.DashboardMetrics) []MetricCard {
	return []MetricCard{
		{Value: metrics.Clusters, Label: "Number of clusters"},
		{Value: metrics.Running, Label: "Running machines"},
		{Value: metrics.Provisioning, Label: "Provisioning machines"},
		{Value: metrics.Total, Label: "Total machines"},
	}
}

//go:embed templates
var templatesFS embed.FS

const (
	htmxRequestHeader = "Hx-Request"
	htmxTrue          = "true"
	pageApp           = "app.html"
	pageClusterDetail = "cluster_detail.html"
)

// ErrTemplateNotFound is returned when a template is not found.
var ErrTemplateNotFound = errors.New("template not found")

// Router handles HTTP routes for the UI.
type Router struct {
	cfg            *config.KommodityConfig
	logger         *zap.Logger
	client         ctrlclient.Client
	clientOnce     sync.Once
	clientErr      error
	kubeClient     *kubernetes.Clientset
	kubeClientOnce sync.Once
	kubeClientErr  error
	pages          map[string]*template.Template
}

// NewRouter creates a new router instance.
func NewRouter(
	cfg *config.KommodityConfig,
	logger *zap.Logger,
) *Router {
	return &Router{
		cfg:    cfg,
		logger: logger,
		pages:  loadTemplates(),
	}
}

// RegisterRoutes registers all UI routes.
func (r *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", r.handleApp)
	mux.HandleFunc("GET /ui/clusters/{clusterName}", r.handleClusterDetail)

	// API routes
	mux.HandleFunc("GET /api/info", r.handleInfo)
	mux.HandleFunc("GET /api/clusters", r.handleClusters)
	mux.HandleFunc("GET /api/clusters/{clusterName}/health", r.handleClusterHealth)
	mux.HandleFunc("GET /api/clusters/{clusterName}/kubeconfig", r.handleClusterKubeconfig)
}

// handleApp renders the dashboard page.
func (r *Router) handleApp(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// Get clients (created lazily on first call)
	client, err := r.getClient()
	if err != nil {
		r.logger.Error("failed to get controller-runtime client", zap.Error(err))
		http.Error(writer, "Failed to initialize Kubernetes client", http.StatusInternalServerError)

		return
	}

	kubeClient, err := r.getKubeClient()
	if err != nil {
		r.logger.Error("failed to get kubernetes client", zap.Error(err))
		http.Error(writer, "Failed to connect to Kubernetes", http.StatusInternalServerError)

		return
	}

	// Get dashboard metrics
	metrics, err := api.GetDashboardMetrics(ctx, client)
	if err != nil {
		r.logger.Error("failed to get dashboard metrics", zap.Error(err))
		http.Error(writer, "Failed to load dashboard metrics", http.StatusInternalServerError)

		return
	}

	// Get cluster list
	clusters, err := api.GetClusterList(ctx, client, kubeClient)
	if err != nil {
		r.logger.Error("failed to get cluster list", zap.Error(err))
		http.Error(writer, "Failed to load cluster list", http.StatusInternalServerError)

		return
	}

	// Get kubeconfig content
	kubeconfigContent, err := api.GetKommodityKubeConfig(r.cfg)
	if err != nil {
		r.logger.Error("failed to get kubeconfig", zap.Error(err))
		http.Error(writer, "Failed to load kubeconfig", http.StatusInternalServerError)

		return
	}

	// Build template data
	data := map[string]any{
		"Title":    "Dashboard",
		"Metrics":  buildDashboardMetricCards(metrics),
		"Clusters": clusters,
		"Version":  getKommodityVersion(),
		"KubeconfigSection": KubeconfigSection{
			ID:      "kommodity",
			Content: kubeconfigContent,
		},
	}

	// Render full page or content only
	err = r.renderPageOrContent(writer, req, pageApp, data)
	if err != nil {
		r.logger.Error("failed to render page", zap.Error(err))
		http.Error(writer, "Failed to render page", http.StatusInternalServerError)
	}
}

// handleInfo godoc
// @Summary  Obtain Kommodity system information
// @Tags     UI, Info
// @Success  200  {object}  map[string]string
// @Failure  500  {object}  string   "If there is a server error"
// @Produce  json
// @Router   /api/info [get]
//
// handleInfo returns basic system information such as version.
func (r *Router) handleInfo(writer http.ResponseWriter, _ *http.Request) {
	info := map[string]string{
		"version": getKommodityVersion(),
	}

	writer.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(writer).Encode(info)
	if err != nil {
		r.logger.Error("failed to encode info response", zap.Error(err))
		http.Error(writer, "Failed to encode response", http.StatusInternalServerError)

		return
	}
}

// handleClusters godoc
// @Summary  List all clusters with their chart versions
// @Tags     Clusters
// @Success  200  {object}  api.ClusterAPIResponse
// @Failure  500  {object}  string   "If there is a server error"
// @Produce  json
// @Router   /api/clusters [get]
//
// handleClusters returns a list of all clusters with their chart versions.
func (r *Router) handleClusters(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	client, err := r.getClient()
	if err != nil {
		r.logger.Error("failed to get controller-runtime client", zap.Error(err))
		http.Error(writer, "Failed to initialize Kubernetes client", http.StatusInternalServerError)

		return
	}

	kubeClient, err := r.getKubeClient()
	if err != nil {
		r.logger.Error("failed to get kubernetes client", zap.Error(err))
		http.Error(writer, "Failed to connect to Kubernetes", http.StatusInternalServerError)

		return
	}

	clusterInfos, err := api.GetClusterList(ctx, client, kubeClient)
	if err != nil {
		r.logger.Error("failed to get cluster list", zap.Error(err))
		http.Error(writer, "Failed to load cluster list", http.StatusInternalServerError)

		return
	}

	response := api.TransformClusterInfoToAPI(clusterInfos)

	writer.Header().Set("Content-Type", "application/json")

	err = json.NewEncoder(writer).Encode(response)
	if err != nil {
		r.logger.Error("failed to encode clusters response", zap.Error(err))
		http.Error(writer, "Failed to encode response", http.StatusInternalServerError)

		return
	}
}

// handleClusterHealth godoc
// @Summary  Checks the health of a cluster by accessing its /livez endpoint
// @Tags     UI, Info, Health
// @Param    clusterName  path  string  true  "Name of the cluster to check health for"
// @Success  200  {object}  api.ClusterHealthResponse
// @Failure  400  {object}  string   "If the cluster name is missing or invalid"
// @Failure  500  {object}  string   "If there is a server error"
// @Produce  json
// @Router   /api/clusters/{clusterName}/health [get]
//
// handleClusterHealth returns health information for a specific cluster.
func (r *Router) handleClusterHealth(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	clusterName := req.PathValue("clusterName")

	if clusterName == "" {
		http.Error(writer, "Cluster name is required", http.StatusBadRequest)

		return
	}

	kubeconfigBytes, err := api.GetClusterKubeconfigBytes(ctx, r.cfg, clusterName)
	if err != nil {
		r.logger.Warn("failed to get cluster kubeconfig for health check",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)
		http.Error(writer, "Unable to retrieve cluster configuration", http.StatusInternalServerError)

		return
	}

	healthy, reason := api.CheckClusterLivez(ctx, kubeconfigBytes, r.logger)

	response := api.ClusterHealthResponse{
		Healthy: healthy,
		Reason:  reason,
	}

	writer.Header().Set("Content-Type", "application/json")

	err = json.NewEncoder(writer).Encode(response)
	if err != nil {
		r.logger.Error("failed to encode health response", zap.Error(err))
		http.Error(writer, "Failed to encode response", http.StatusInternalServerError)

		return
	}
}

// handleClusterKubeconfig godoc
// @Summary  Get kubeconfig for a specific cluster
// @Tags     Clusters, Kubeconfig
// @Param    clusterName  path  string  true  "Name of the cluster to get kubeconfig for"
// @Success  200  {string}  string  "Kubeconfig YAML content"
// @Failure  400  {object}  string   "If the cluster name is missing or invalid"
// @Failure  403  {object}  string   "If OIDC is not configured on the cluster"
// @Failure  404  {object}  string   "If the cluster is not found"
// @Failure  500  {object}  string   "If there is a server error"
// @Produce  application/yaml
// @Router   /api/clusters/{clusterName}/kubeconfig [get]
//
// handleClusterKubeconfig returns the kubeconfig content for a specific cluster.
func (r *Router) handleClusterKubeconfig(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	clusterName := req.PathValue("clusterName")

	if clusterName == "" {
		http.Error(writer, "Cluster name is required", http.StatusBadRequest)

		return
	}

	kubeconfigContent, err := api.GetClusterKubeconfigContent(ctx, r.cfg, clusterName)
	if err != nil {
		if errors.Is(err, api.ErrOIDCNotConfigured) {
			http.Error(writer, "OIDC is not configured on the cluster", http.StatusForbidden)

			return
		}

		if errors.Is(err, api.ErrAuthConfigDisabled) {
			http.Error(writer, "Auth config application is disabled", http.StatusForbidden)

			return
		}

		r.logger.Warn("failed to get cluster kubeconfig",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)
		http.Error(writer, "Failed to retrieve kubeconfig", http.StatusInternalServerError)

		return
	}

	writer.Header().Set("Content-Type", "application/yaml; charset=utf-8")

	//nolint:gosec // kubeconfig is YAML content, not HTML
	_, err = writer.Write([]byte(kubeconfigContent))
	if err != nil {
		r.logger.Error("failed to write kubeconfig response", zap.Error(err))
	}
}

// handleClusterDetail renders the cluster detail page.
func (r *Router) handleClusterDetail(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	clusterName := req.PathValue("clusterName")

	if clusterName == "" {
		http.Error(writer, "Cluster name is required", http.StatusBadRequest)

		return
	}

	// Get clients (created lazily on first call)
	client, err := r.getClient()
	if err != nil {
		r.logger.Error("failed to get controller-runtime client", zap.Error(err))
		http.Error(writer, "Failed to initialize Kubernetes client", http.StatusInternalServerError)

		return
	}

	kubeClient, err := r.getKubeClient()
	if err != nil {
		r.logger.Error("failed to get kubernetes client", zap.Error(err))
		http.Error(writer, "Failed to connect to Kubernetes", http.StatusInternalServerError)

		return
	}

	// Get cluster detail
	clusterDetail, err := api.GetClusterDetail(ctx, client, kubeClient, clusterName, r.logger)
	if err != nil {
		// Return 404 for cluster not found, 500 for other errors
		if errors.Is(err, api.ErrClusterNotFound) {
			http.Error(writer, fmt.Sprintf("Cluster %s not found", clusterName), http.StatusNotFound)

			return
		}

		r.logger.Error("failed to get cluster detail",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)
		http.Error(writer, "Failed to load cluster details", http.StatusInternalServerError)

		return
	}

	// Get kubeconfig content for this cluster
	kubeconfigContent, err := api.GetClusterKubeconfigContent(ctx, r.cfg, clusterName)
	if err != nil {
		r.logger.Warn("failed to get cluster kubeconfig",
			zap.String("cluster", clusterName),
			zap.Error(err),
		)

		kubeconfigContent = "# Kubeconfig not available"
	}

	// Build and render template data
	data := buildClusterDetailData(clusterName, clusterDetail, kubeconfigContent)

	err = r.renderPageOrContent(writer, req, pageClusterDetail, data)
	if err != nil {
		r.logger.Error("failed to render cluster detail page", zap.Error(err))
		http.Error(writer, "Failed to render page", http.StatusInternalServerError)
	}
}

// buildClusterDetailData builds the template data for the cluster detail page.
func buildClusterDetailData(
	clusterName string,
	clusterDetail *api.ClusterDetail,
	kubeconfigContent string,
) map[string]any {
	// Calculate total machines and check if any deployment has autoscaler
	totalMachines := 0
	hasAutoscaler := false

	for _, deployment := range clusterDetail.MachineDeployments {
		totalMachines += len(deployment.Machines)

		if deployment.MinSize != nil {
			hasAutoscaler = true
		}
	}

	clusterMetrics := []struct {
		Value string
		Label string
	}{
		{Value: clusterDetail.ChartVersion, Label: "Chart Version"},
		{Value: clusterDetail.KubernetesVersion, Label: "Kubernetes Version"},
		{Value: clusterDetail.TalosVersion, Label: "Talos Version"},
		{Value: strconv.Itoa(totalMachines), Label: "Total Machines"},
	}

	return map[string]any{
		"Title":          "Cluster: " + clusterName,
		"Cluster":        clusterDetail,
		"ClusterName":    clusterName,
		"ClusterMetrics": clusterMetrics,
		"HasAutoscaler":  hasAutoscaler,
		"Version":        getKommodityVersion(),
		"KubeconfigSection": KubeconfigSection{
			ID:      clusterName,
			Content: kubeconfigContent,
		},
	}
}

// getClient returns the controller-runtime client, creating it on first call.
func (r *Router) getClient() (ctrlclient.Client, error) {
	r.clientOnce.Do(func() {
		// Create scheme with required types
		scheme := runtime.NewScheme()

		// Add schemes needed for UI
		err := r.enhanceSchemeForUI(scheme)
		if err != nil {
			r.clientErr = fmt.Errorf("failed to enhance scheme: %w", err)

			return
		}

		// Create client using loopback config
		client, err := ctrlclient.New(
			r.cfg.ClientConfig.LoopbackClientConfig,
			ctrlclient.Options{Scheme: scheme},
		)
		if err != nil {
			r.clientErr = fmt.Errorf("failed to create controller-runtime client: %w", err)

			return
		}

		r.client = client
	})

	return r.client, r.clientErr
}

// getKubeClient returns the Kubernetes clientset, creating it on first call.
func (r *Router) getKubeClient() (*kubernetes.Clientset, error) {
	r.kubeClientOnce.Do(func() {
		kubeClient, err := kubernetes.NewForConfig(r.cfg.ClientConfig.LoopbackClientConfig)
		if err != nil {
			r.kubeClientErr = fmt.Errorf("failed to create kubernetes client: %w", err)

			return
		}

		r.kubeClient = kubeClient
	})

	return r.kubeClient, r.kubeClientErr
}

// enhanceSchemeForUI adds all schemes needed for the UI client.
func (r *Router) enhanceSchemeForUI(scheme *runtime.Scheme) error {
	addFuncs := []struct {
		name string
		fn   func(*runtime.Scheme) error
	}{
		{"clientgoscheme.AddToScheme", clientgoscheme.AddToScheme},
		{"clusterv1.AddToScheme", clusterv1.AddToScheme},
		{"taloscontrolplanev1.AddToScheme", taloscontrolplanev1.AddToScheme},
	}

	for _, add := range addFuncs {
		err := add.fn(scheme)
		if err != nil {
			return fmt.Errorf("failed to add %s: %w", add.name, err)
		}
	}

	return nil
}

// renderPageOrContent renders either the full page with layout or just the content.
func (r *Router) renderPageOrContent(
	writer http.ResponseWriter,
	req *http.Request,
	pageName string,
	data any,
) error {
	if req.Header.Get(htmxRequestHeader) == htmxTrue {
		return r.renderContent(writer, pageName, data)
	}

	return r.renderPage(writer, pageName, data)
}

// renderPage renders the full page with layout.
func (r *Router) renderPage(
	writer http.ResponseWriter,
	pageName string,
	data any,
) error {
	return r.renderTemplate(writer, pageName, "layout.html", data)
}

// renderContent renders just the content block for HTMX requests.
func (r *Router) renderContent(
	writer http.ResponseWriter,
	pageName string,
	data any,
) error {
	return r.renderTemplate(writer, pageName, "content", data)
}

// renderTemplate renders a specific template block.
func (r *Router) renderTemplate(
	writer http.ResponseWriter,
	pageName string,
	blockName string,
	data any,
) error {
	tmpl, ok := r.pages[pageName]
	if !ok {
		return ErrTemplateNotFound
	}

	writer.Header().Set("Content-Type", "text/html; charset=utf-8")

	err := tmpl.ExecuteTemplate(writer, blockName, data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

// loadTemplates loads and parses all templates.
func loadTemplates() map[string]*template.Template {
	// Shared components used by all pages
	shared := []string{
		"templates/layout.html",
		"templates/components/kubeconfig_section.html",
		"templates/components/metrics_cards.html",
		"templates/components/cluster_table.html",
		"templates/components/icon_copy.html",
		"templates/components/icon_download.html",
		"templates/components/icon_chevron.html",
		"templates/components/health_tooltip.html",
		"templates/components/health_indicator.js.html",
	}

	return map[string]*template.Template{
		pageApp:           mustParsePage(shared, "templates/app.html"),
		pageClusterDetail: mustParsePage(shared, "templates/cluster_detail.html"),
	}
}

// mustParsePage parses a page template with shared components.
func mustParsePage(
	shared []string,
	page string,
) *template.Template {
	files := make([]string, len(shared)+1)
	copy(files, shared)
	files[len(shared)] = page

	return template.Must(
		template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, files...),
	)
}

func getKommodityVersion() string {
	versionInfo := componentbaseversion.Get()
	if versionInfo.GitVersion == "" {
		return "development"
	}

	return versionInfo.GitVersion
}
