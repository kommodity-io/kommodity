// Package genericserver contains the plumbing for a server
// that can handle both gRPC and REST requests.
package genericserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/kommodity-io/kommodity/pkg/encoding"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

// Factory is a function that initializes the server.
type Factory func() error

// HTTPMuxFactory is a function that initializes the HTTP mux.
type HTTPMuxFactory func(*http.ServeMux) error

// GRPCServerFactory is a function that initializes the gRPC server.
type GRPCServerFactory func(*grpc.Server) error

// HTTPServer is a struct that contains the HTTP server configuration.
type HTTPServer struct {
	server    *http.Server
	listener  net.Listener
	mux       *http.ServeMux
	factories []Factory
}

// GRPCServer is a struct that contains the gRPC server configuration.
type GRPCServer struct {
	server    *grpc.Server
	listener  net.Listener
	factories []Factory
}

// MuxServer is a struct that contains the cmux server configuration.
type MuxServer struct {
	cmux     cmux.CMux
	listener net.Listener
}

// GenericServer is a struct that contains the server configuration.
type GenericServer struct {
	muxServer   *MuxServer
	grpcServer  *GRPCServer
	httpServer  *HTTPServer
	logger      *zap.Logger
	port        int
	ready       bool
	apiGroups   []metav1.APIGroup
	versionInfo *version.Info
	sync.RWMutex
}

// New creates a new server instance.
func New(ctx context.Context, opts ...Option) *GenericServer {
	srv := &GenericServer{
		muxServer: &MuxServer{
			cmux:     nil,
			listener: nil,
		},
		httpServer: &HTTPServer{
			server:    nil,
			listener:  nil,
			factories: []Factory{},
			mux:       http.NewServeMux(),
		},
		grpcServer: &GRPCServer{
			server:    grpc.NewServer(),
			listener:  nil,
			factories: []Factory{},
		},
		logger: zap.L(),
		port:   getPort(ctx),
		versionInfo: &version.Info{
			Major:        "1",
			Minor:        "0",
			GitVersion:   "v1.0.0",
			GitCommit:    "unknown",
			GitTreeState: "clean",
			BuildDate:    time.Now().UTC().Format(time.RFC3339),
			GoVersion:    runtime.Version(),
			Compiler:     runtime.Compiler,
			Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		},
	}

	for _, opt := range opts {
		opt(srv)
	}

	return srv
}

// ListenAndServe starts the server and listens for incoming requests.
// It initializes the HTTP and gRPC servers and starts the cmux server.
// The HTTP server is wrapped with h2c support to allow HTTP/2 connections.
// The gRPC server is registered with reflection to allow for introspection.
func (s *GenericServer) ListenAndServe(_ context.Context) error {
	for _, factory := range s.httpServer.factories {
		if err := factory(); err != nil {
			s.logger.Error("Failed to initialize HTTP server", zap.Error(err))

			return err
		}
	}

	for _, factory := range s.grpcServer.factories {
		if err := factory(); err != nil {
			s.logger.Error("Failed to initialize gRPC server", zap.Error(err))

			return err
		}
	}

	muxListener, err := net.Listen("tcp", ":"+strconv.Itoa(s.port))
	if err != nil {
		return fmt.Errorf("failed to start cmux listener: %w", err)
	}

	s.muxServer.listener = muxListener
	s.muxServer.cmux = cmux.New(muxListener)

	s.grpcServer.listener = s.muxServer.cmux.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"),
	)
	s.httpServer.listener = s.muxServer.cmux.Match(cmux.Any())

	go s.serveHTTP()
	go s.serveGRPC()

	s.setReady(true)

	s.logger.Info("Starting cmux server", zap.Int("port", s.port))

	if err := s.muxServer.cmux.Serve(); err != nil {
		// This is expected when the server is shut down gracefully.
		// Reference: https://github.com/soheilhy/cmux/pull/92
		if !errors.Is(err, net.ErrClosed) {
			s.logger.Error("Failed to run cmux server", zap.Error(err), zap.Int("port", s.port))

			return fmt.Errorf("failed to run cmux server: %w", err)
		}

		s.logger.Info("Closed cmux listener", zap.Int("port", s.port))
	}

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *GenericServer) Shutdown(ctx context.Context) error {
	s.setReady(false)

	if s.muxServer.cmux != nil {
		s.logger.Info("Shutting down cmux server", zap.Int("port", s.port))

		s.muxServer.cmux.Close()
	}

	if s.grpcServer.server != nil {
		s.logger.Info("Shutting down gRPC server", zap.Int("port", s.port))

		s.grpcServer.server.GracefulStop()

		s.logger.Info("Shut down gRPC server", zap.Int("port", s.port))
	}

	if s.httpServer.server != nil {
		s.httpServer.server.SetKeepAlivesEnabled(false)

		s.logger.Info("Shutting down HTTP server", zap.Int("port", s.port))

		if err := s.httpServer.server.Shutdown(ctx); err != nil {
			// This is expected when the server is shut down via cmux.
			// Reference: https://github.com/soheilhy/cmux/pull/92
			if errors.Is(err, net.ErrClosed) {
				s.logger.Info("Shut down HTTP server", zap.Int("port", s.port))

				return nil
			}
		}

		s.logger.Info("Shut down HTTP server", zap.Int("port", s.port))
	}

	return nil
}

// InstallAPIGroup installs the API group into the server.
func (s *GenericServer) InstallAPIGroup(apiGroupInfo *genericapiserver.APIGroupInfo) error {
	// Add a factory that installs this API group
	factory := func() error {
		// Use the first prioritized version's group name for logging
		groupName := apiGroupInfo.PrioritizedVersions[0].Group
		s.logger.Info("Installing API group", zap.String("group", groupName))

		// Create API group for discovery
		versions := []metav1.GroupVersionForDiscovery{}
		for _, groupVersion := range apiGroupInfo.PrioritizedVersions {
			versions = append(versions, metav1.GroupVersionForDiscovery{
				GroupVersion: groupVersion.String(),
				Version:      groupVersion.Version,
			})
		}

		apiGroup := metav1.APIGroup{
			Name:     groupName,
			Versions: versions,
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: apiGroupInfo.PrioritizedVersions[0].String(),
				Version:      apiGroupInfo.PrioritizedVersions[0].Version,
			},
		}

		// Store the API group for discovery
		s.Lock()
		s.apiGroups = append(s.apiGroups, apiGroup)
		s.Unlock()

		if err := s.newAPIGroupFactory(apiGroupInfo)(s.httpServer.mux); err != nil {
			return fmt.Errorf("failed to install API group %s: %w", groupName, err)
		}

		s.logger.Info("Installed API group", zap.String("group", groupName))

		return nil
	}

	s.httpServer.factories = append(s.httpServer.factories, factory)

	return nil
}

// SetVersion sets the version information for the server.
// This only accepts GitVersion and GitCommit from the version.Info struct.
// All other fields are automatically computed based on the runtime information.
func (s *GenericServer) SetVersion(info *version.Info) {
	s.Lock()
	defer s.Unlock()

	// Strip the "v" prefix if it exists and parse the semantic version.
	semantic, err := semver.Parse(strings.TrimPrefix(info.GitVersion, "v"))
	if err != nil {
		s.logger.Warn("Failed to parse semantic version", zap.String("version", info.GitVersion), zap.Error(err))

		semantic = semver.Version{
			Major: 0,
			Minor: 0,
		}
	}

	treeState := "clean"
	if strings.HasSuffix(info.GitVersion, "-dirty") {
		treeState = "dirty"
	}

	s.versionInfo = &version.Info{
		Major:        strconv.FormatUint(semantic.Major, 10),
		Minor:        strconv.FormatUint(semantic.Minor, 10),
		GitVersion:   info.GitVersion,
		GitCommit:    info.GitCommit,
		GitTreeState: treeState,
		BuildDate:    time.Now().UTC().Format(time.RFC3339),
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// APIVersionHandler handles requests for API resources.
type APIVersionHandler struct {
	groupVersion                 schema.GroupVersion
	storage                      map[string]rest.Storage
	serializer                   k8sruntime.NegotiatedSerializer
	minRequestTimeout            time.Duration
	enableAPIResponseCompression bool
}

// ServeHTTP handles API requests. This is a mock implementation.
// In a full implementation, this would:
// 1. Parse the URL to extract the resource and name.
// 2. Get the appropriate storage.
// 3. Use the storage to handle the request (get, list, create, update, delete).
// 4. Serialize the response.
func (h *APIVersionHandler) ServeHTTP(res http.ResponseWriter, _ *http.Request) {
	res.Header().Set("Content-Type", "application/json")

	status := &metav1.Status{
		Status:  "Failure",
		Code:    int32(http.StatusNotImplemented),
		Reason:  metav1.StatusReasonNotFound,
		Message: "API endpoint not implemented yet",
	}

	res.WriteHeader(http.StatusNotImplemented)

	if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
	}
}

// getVerbs returns the supported verbs for the given storage.
func getVerbs(storage rest.Storage) []string {
	verbs := []string{}

	if _, ok := storage.(rest.Getter); ok {
		verbs = append(verbs, "get")
	}

	if _, ok := storage.(rest.Lister); ok {
		verbs = append(verbs, "list")
	}

	//nolint:misspell // Creater is the correct term used in the Kubernetes API.
	if _, ok := storage.(rest.Creater); ok {
		verbs = append(verbs, "create")
	}

	if _, ok := storage.(rest.Updater); ok {
		verbs = append(verbs, "update")
	}

	if _, ok := storage.(rest.GracefulDeleter); ok {
		verbs = append(verbs, "delete")
	}

	if _, ok := storage.(rest.CollectionDeleter); ok {
		verbs = append(verbs, "deletecollection")
	}

	if _, ok := storage.(rest.Watcher); ok {
		verbs = append(verbs, "watch")
	}

	if _, ok := storage.(rest.Patcher); ok {
		verbs = append(verbs, "patch")
	}

	return verbs
}

// newAPIGroupFactory creates a new factory function that initializes the API group.
func (s *GenericServer) newAPIGroupFactory(apiGroupInfo *genericapiserver.APIGroupInfo) HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		// Use the first prioritized version's group name for logging
		groupName := apiGroupInfo.PrioritizedVersions[0].Group

		// Register the API group and versions in the HTTP mux.
		prefix := "/apis/" + groupName

		// Register API resources for each version.
		for _, groupVersion := range apiGroupInfo.PrioritizedVersions {
			versionHandler := &APIVersionHandler{
				groupVersion:                 groupVersion,
				storage:                      apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version],
				serializer:                   apiGroupInfo.NegotiatedSerializer,
				minRequestTimeout:            1 * time.Minute,
				enableAPIResponseCompression: true,
			}

			versionPath := prefix + "/" + groupVersion.Version

			// Install handlers for the version.
			mux.Handle(versionPath+"/", versionHandler)

			// Register discovery information for this version.
			mux.HandleFunc(versionPath, func(res http.ResponseWriter, _ *http.Request) {
				resources := []metav1.APIResource{}

				for resource, storage := range apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version] {
					// Skip subresources.
					if strings.Contains(resource, "/") {
						continue
					}

					namespaced := false
					if scoper, ok := storage.(rest.Scoper); ok {
						namespaced = scoper.NamespaceScoped()
					}

					// Try to get kind information.
					kind := ""
					newFunc := storage.New()

					if newFunc != nil {
						kind = newFunc.GetObjectKind().GroupVersionKind().Kind
					}

					resources = append(resources, metav1.APIResource{
						Name:       resource,
						Namespaced: namespaced,
						Kind:       kind,
						Verbs:      getVerbs(storage),
					})
				}

				resourceList := &metav1.APIResourceList{
					GroupVersion: groupVersion.String(),
					APIResources: resources,
				}

				res.Header().Set("Content-Type", "application/json")

				if err := encoding.NewKubeJSONEncoder(res).Encode(resourceList); err != nil {
					s.logger.Error("Failed to encode API resource list", zap.Error(err))
					http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
				}
			})
		}

		// Register API group discovery information.
		mux.HandleFunc(prefix, func(res http.ResponseWriter, _ *http.Request) {
			versions := []metav1.GroupVersionForDiscovery{}

			for _, groupVersion := range apiGroupInfo.PrioritizedVersions {
				versions = append(versions, metav1.GroupVersionForDiscovery{
					GroupVersion: groupVersion.String(),
					Version:      groupVersion.Version,
				})
			}

			apiGroup := &metav1.APIGroup{
				Name:     groupName,
				Versions: versions,
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: apiGroupInfo.PrioritizedVersions[0].String(),
					Version:      apiGroupInfo.PrioritizedVersions[0].Version,
				},
			}

			res.Header().Set("Content-Type", "application/json")

			if err := encoding.NewKubeJSONEncoder(res).Encode(apiGroup); err != nil {
				s.logger.Error("Failed to encode API group", zap.Error(err))
				http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
			}
		})

		return nil
	}
}

// setReady sets the server's readiness state.
func (s *GenericServer) setReady(ready bool) {
	s.Lock()
	defer s.Unlock()

	s.ready = ready
}

// serveHTTP starts the HTTP server and listens for incoming requests.
func (s *GenericServer) serveHTTP() {
	// Register standard health endpoints
	s.httpServer.mux.HandleFunc("/readyz", s.readyz)
	s.httpServer.mux.HandleFunc("/livez", s.livez)

	// Register the Kubernetes-compatible version endpoint
	s.httpServer.mux.HandleFunc("/version", s.versionHandler)

	// Register the API discovery endpoint
	s.httpServer.mux.HandleFunc("/apis", s.listAPIGroups)
	s.httpServer.mux.HandleFunc("/apis/", s.listAPIGroups)

	s.httpServer.server = &http.Server{
		// Wrap the HTTP handler to provide h2c support.
		Handler: h2c.NewHandler(s.httpServer.mux, &http2.Server{}),
		// This prevents slowloris attacks, but may be rather aggressive.
		ReadHeaderTimeout: 1 * time.Second,
	}

	s.logger.Info("Starting HTTP server", zap.Int("port", s.port))

	if err := s.httpServer.server.Serve(s.httpServer.listener); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// This is expected when the server is shut down gracefully.
			return
		}

		if errors.Is(err, cmux.ErrServerClosed) {
			// This is expected when the server is shut down via cmux.
			return
		}

		s.logger.Error("Failed to run HTTP server", zap.Error(err), zap.Int("port", s.port))
	}
}

// serveGRPC starts the gRPC server and listens for incoming requests.
func (s *GenericServer) serveGRPC() {
	// Allow reflection to enable tools like grpcurl.
	reflection.Register(s.grpcServer.server)

	s.logger.Info("Starting gRPC server", zap.Int("port", s.port))

	if err := s.grpcServer.server.Serve(s.grpcServer.listener); err != nil {
		// This is expected when the server is shut down gracefully.
		if !errors.Is(err, grpc.ErrServerStopped) {
			s.logger.Error("Failed to run gRPC server", zap.Error(err), zap.Int("port", s.port))
		}
	}
}

// readyz checks if the server is ready to serve requests.
func (s *GenericServer) readyz(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	if !s.ready {
		code := http.StatusServiceUnavailable
		status := &metav1.Status{
			Status:  "Failure",
			Code:    int32(code),
			Message: "Not ready to serve requests",
			Reason:  metav1.StatusReason(http.StatusText(code)),
		}

		res.WriteHeader(code)

		if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
			s.logger.Error("Failed to encode status", zap.Error(err))

			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

			return
		}

		return
	}

	code := http.StatusOK
	status := &metav1.Status{
		Status:  "Success",
		Code:    int32(code),
		Reason:  metav1.StatusReason(http.StatusText(code)),
		Message: "Ready to serve requests",
	}

	res.WriteHeader(code)

	if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
		s.logger.Error("Failed to encode status", zap.Error(err))

		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

		return
	}
}

// livez checks if the server is alive and running.
func (s *GenericServer) livez(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	code := http.StatusOK
	status := &metav1.Status{
		Status:  "Success",
		Code:    int32(code),
		Reason:  metav1.StatusReason(http.StatusText(code)),
		Message: "Server running",
	}

	res.WriteHeader(code)

	if err := encoding.NewKubeJSONEncoder(res).Encode(status); err != nil {
		s.logger.Error("Failed to encode status", zap.Error(err))

		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)

		return
	}
}

// listAPIGroups handles requests to list all available API groups.
func (s *GenericServer) listAPIGroups(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	// Create the APIGroupList response
	apiGroupList := &metav1.APIGroupList{
		Groups: s.apiGroups,
	}

	res.Header().Set("Content-Type", "application/json")

	if err := encoding.NewKubeJSONEncoder(res).Encode(apiGroupList); err != nil {
		s.logger.Error("Failed to encode API group list", zap.Error(err))
		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
	}
}

// versionHandler handles requests for the /version endpoint.
func (s *GenericServer) versionHandler(res http.ResponseWriter, _ *http.Request) {
	s.RLock()
	defer s.RUnlock()

	res.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(res).Encode(s.versionInfo); err != nil {
		s.logger.Error("Failed to encode version info", zap.Error(err))
		http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
	}
}
