package ui

import (
	"embed"
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
	cfg        *config.KommodityConfig
	logger     *zap.Logger
	client     ctrlclient.Client
	clientOnce sync.Once
	clientErr  error
	pages      map[string]*template.Template
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
	mux.HandleFunc("GET /app/clusters/{clusterName}", r.handleClusterDetail)

	// API routes
	mux.HandleFunc("GET /api/cluster/{clusterName}/health", api.GetClusterHealth(r.cfg, r.logger))
}

// handleApp renders the dashboard page.
func (r *Router) handleApp(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// Get client (created lazily on first call)
	client, err := r.getClient()
	if err != nil {
		r.logger.Error("failed to get controller-runtime client", zap.Error(err))
		http.Error(writer, "Failed to initialize Kubernetes client", http.StatusInternalServerError)

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
	kubeClient, err := kubernetes.NewForConfig(r.cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		r.logger.Error("failed to get kubernetes client", zap.Error(err))
		http.Error(writer, "Failed to connect to Kubernetes", http.StatusInternalServerError)

		return
	}

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

// handleClusterDetail renders the cluster detail page.
func (r *Router) handleClusterDetail(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	clusterName := req.PathValue("clusterName")

	if clusterName == "" {
		http.Error(writer, "Cluster name is required", http.StatusBadRequest)

		return
	}

	// Get client (created lazily on first call)
	client, err := r.getClient()
	if err != nil {
		r.logger.Error("failed to get controller-runtime client", zap.Error(err))
		http.Error(writer, "Failed to initialize Kubernetes client", http.StatusInternalServerError)

		return
	}

	// Get Kubernetes client
	kubeClient, err := kubernetes.NewForConfig(r.cfg.ClientConfig.LoopbackClientConfig)
	if err != nil {
		r.logger.Error("failed to get kubernetes client", zap.Error(err))
		http.Error(writer, "Failed to connect to Kubernetes", http.StatusInternalServerError)

		return
	}

	// Get cluster detail
	clusterDetail, err := api.GetClusterDetail(ctx, client, kubeClient, clusterName, r.logger)
	if err != nil {
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
	// Calculate total machines
	totalMachines := 0
	for _, deployment := range clusterDetail.MachineDeployments {
		totalMachines += len(deployment.Machines)
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
		"Cluster":        clusterDetail,
		"ClusterName":    clusterName,
		"ClusterMetrics": clusterMetrics,
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
