// Package combinedserver provides a combined gRPC and HTTP server with reverse proxy capabilities.
package combinedserver

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const (
	// HealthzPath is the path for the deprecated health check endpoint.
	// Deprecated: Use LivezPath and ReadyzPath instead.
	HealthzPath = "/healthz"
	// LivezPath is the path for the liveness probe endpoint.
	LivezPath = "/livez"
	// ReadyzPath is the path for the readiness probe endpoint.
	ReadyzPath = "/readyz"

	// Query parameter names.
	verboseParam = "verbose"
	excludeParam = "exclude"

	// Health check result prefixes for verbose output.
	checkOKPrefix     = "[+]"
	checkFailedPrefix = "[-]"

	// individualCheckPathSegments is the expected number of path segments for individual check requests.
	// For example, /livez/ping has 2 segments: ["livez", "ping"].
	individualCheckPathSegments = 2
)

// HealthChecker is an interface for individual health checks.
type HealthChecker interface {
	// Name returns the name of the health check.
	Name() string
	// Check performs the health check and returns an error if unhealthy.
	Check() error
}

// healthCheckRegistry manages a collection of health checks.
type healthCheckRegistry struct {
	mu     sync.RWMutex
	checks []HealthChecker
}

// newHealthCheckRegistry creates a new health check registry.
func newHealthCheckRegistry() *healthCheckRegistry {
	return &healthCheckRegistry{
		checks: make([]HealthChecker, 0),
	}
}

// register adds a health check to the registry.
func (r *healthCheckRegistry) register(check HealthChecker) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.checks = append(r.checks, check)
}

// getChecks returns a copy of all registered health checks.
func (r *healthCheckRegistry) getChecks() []HealthChecker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]HealthChecker, len(r.checks))
	copy(result, r.checks)

	return result
}

// alwaysHealthyCheck is a simple health check that always returns healthy.
type alwaysHealthyCheck struct {
}

func (a *alwaysHealthyCheck) Name() string {
	return "healthy"
}

func (a *alwaysHealthyCheck) Check() error {
	return nil
}

// pingHealthCheck verifies the server is in a running state.
type pingHealthCheck struct {
	stateTracker *ServerStateTracker
}

// newPingHealthCheck creates a new ping health check.
func newPingHealthCheck(stateTracker *ServerStateTracker) *pingHealthCheck {
	return &pingHealthCheck{
		stateTracker: stateTracker,
	}
}

func (p *pingHealthCheck) Name() string {
	return "ping"
}

// Check verifies the server is running and responsive.
// Returns an error if the server is not in the running state.
func (p *pingHealthCheck) Check() error {
	state := p.stateTracker.GetState()
	if state != ServerStateRunning {
		return fmt.Errorf("%w: server is %s", ErrServerNotRunning, state.String())
	}

	return nil
}

// healthHandler handles health check requests with support for verbose and exclude parameters.
type healthHandler struct {
	registry *healthCheckRegistry
	name     string
}

// newHealthHandler creates a new health handler.
func newHealthHandler(registry *healthCheckRegistry, name string) *healthHandler {
	return &healthHandler{
		registry: registry,
		name:     name,
	}
}

// ServeHTTP handles health check HTTP requests.
func (h *healthHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// Check for individual health check path (e.g., /livez/ping)
	pathParts := strings.Split(strings.TrimPrefix(request.URL.Path, "/"), "/")
	if len(pathParts) == individualCheckPathSegments {
		h.handleIndividualCheck(writer, pathParts[1])

		return
	}

	verbose := request.URL.Query().Has(verboseParam)

	excludes := make(map[string]bool)
	for _, exclude := range request.URL.Query()[excludeParam] {
		excludes[exclude] = true
	}

	h.handleAggregatedCheck(writer, verbose, excludes)
}

// handleIndividualCheck handles requests for a specific health check.
func (h *healthHandler) handleIndividualCheck(writer http.ResponseWriter, checkName string) {
	checks := h.registry.getChecks()

	for _, check := range checks {
		if check.Name() == checkName {
			h.writeCheckResult(writer, check)

			return
		}
	}

	http.NotFound(writer, nil)
}

// writeCheckResult writes the result of a single health check to the response.
func (h *healthHandler) writeCheckResult(writer http.ResponseWriter, check HealthChecker) {
	err := check.Check()
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(writer, "%s%s failed: %v\n", checkFailedPrefix, check.Name(), err)

		return
	}

	writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(writer, "%s%s ok\n", checkOKPrefix, check.Name())
}

// handleAggregatedCheck handles requests that aggregate all health checks.
func (h *healthHandler) handleAggregatedCheck(
	writer http.ResponseWriter,
	verbose bool,
	excludes map[string]bool,
) {
	checks := h.registry.getChecks()
	result := h.runAllChecks(checks, excludes, verbose)

	h.writeAggregatedResponse(writer, result, verbose)
}

// checkResult holds the result of running all health checks.
type checkResult struct {
	output     strings.Builder
	allHealthy bool
}

// runAllChecks executes all health checks and collects results.
func (h *healthHandler) runAllChecks(
	checks []HealthChecker,
	excludes map[string]bool,
	verbose bool,
) checkResult {
	result := checkResult{allHealthy: true}

	for _, check := range checks {
		h.runSingleCheck(check, excludes, verbose, &result)
	}

	if verbose {
		h.appendSummary(&result)
	}

	return result
}

// runSingleCheck executes a single health check and updates the result.
func (h *healthHandler) runSingleCheck(
	check HealthChecker,
	excludes map[string]bool,
	verbose bool,
	result *checkResult,
) {
	name := check.Name()

	if excludes[name] {
		if verbose {
			_, _ = fmt.Fprintf(&result.output, "%s%s excluded: ok\n", checkOKPrefix, name)
		}

		return
	}

	err := check.Check()
	if err != nil {
		result.allHealthy = false

		if verbose {
			_, _ = fmt.Fprintf(&result.output, "%s%s failed: %v\n", checkFailedPrefix, name, err)
		}

		return
	}

	if verbose {
		_, _ = fmt.Fprintf(&result.output, "%s%s ok\n", checkOKPrefix, name)
	}
}

// appendSummary appends the final summary line to the check result.
func (h *healthHandler) appendSummary(result *checkResult) {
	if result.allHealthy {
		_, _ = fmt.Fprintf(&result.output, "%s check passed\n", h.name)
	} else {
		_, _ = fmt.Fprintf(&result.output, "%s check failed\n", h.name)
	}
}

// writeAggregatedResponse writes the aggregated health check response.
func (h *healthHandler) writeAggregatedResponse(
	writer http.ResponseWriter,
	result checkResult,
	verbose bool,
) {
	if result.allHealthy {
		writer.WriteHeader(http.StatusOK)
		h.writeResponseBody(writer, result.output.String(), "ok", verbose)
	} else {
		writer.WriteHeader(http.StatusInternalServerError)
		h.writeResponseBody(writer, result.output.String(), "failed", verbose)
	}
}

// writeResponseBody writes either verbose output or simple status to the response.
func (h *healthHandler) writeResponseBody(
	writer http.ResponseWriter,
	verboseOutput string,
	simpleOutput string,
	verbose bool,
) {
	if verbose {
		_, _ = writer.Write([]byte(verboseOutput))
	} else {
		_, _ = writer.Write([]byte(simpleOutput))
	}
}

// registerHealthChecks registers health check endpoints on the given mux.
func registerHealthChecks(mux *http.ServeMux, stateTracker *ServerStateTracker) {
	// Create registries for liveness and readiness checks
	livezRegistry := newHealthCheckRegistry()
	readyzRegistry := newHealthCheckRegistry()

	// Register always-healthy check for both liveness and readiness, and ping check for readiness only
	livezRegistry.register(&alwaysHealthyCheck{})

	readyzRegistry.register(&alwaysHealthyCheck{})
	readyzRegistry.register(newPingHealthCheck(stateTracker))

	// Create handlers
	livezHandler := newHealthHandler(livezRegistry, "livez")
	readyzHandler := newHealthHandler(readyzRegistry, "readyz")

	// Register endpoints
	// /livez and /livez/{checkName}
	mux.Handle(LivezPath, livezHandler)
	mux.Handle(LivezPath+"/", livezHandler)

	// /readyz and /readyz/{checkName}
	mux.Handle(ReadyzPath, readyzHandler)
	mux.Handle(ReadyzPath+"/", readyzHandler)

	// /healthz is deprecated but still supported, delegates to livez
	mux.Handle(HealthzPath, livezHandler)
	mux.Handle(HealthzPath+"/", livezHandler)
}
