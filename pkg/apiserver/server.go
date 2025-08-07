// Package apiserver provides the implementation of a Kubernetes API server.
package apiserver

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/encoding"
	"github.com/kommodity-io/kommodity/pkg/genericserver"
	"github.com/kommodity-io/kommodity/pkg/kms"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
)

// ResourceHandlerProvider defines a function type that provides storage for a resource.
type ResourceHandlerProvider func(s *runtime.Scheme, g genericregistry.RESTOptionsGetter) (rest.Storage, error)

// ResourceProvider ensures different versions of the same resource share storage.
type ResourceProvider struct {
	Provider ResourceHandlerProvider
}

// Server builds a new apiserver.
type Server struct {
	storageProvider      map[schema.GroupVersionResource]*ResourceProvider
	groupVersions        map[schema.GroupVersion]bool
	orderedGroupVersions []schema.GroupVersion
	schemes              []*runtime.Scheme
	schemeBuilder        runtime.SchemeBuilder
}

// NewAPIServer creates a new API server instance.
func NewAPIServer() *Server {
	return &Server{
		storageProvider: make(map[schema.GroupVersionResource]*ResourceProvider),
	}
}

// Build creates a new api server and generic server with the configured resources and handlers.
func (s *Server) Build(ctx context.Context) (*genericserver.GenericServer, error) {
	scheme := runtime.NewScheme()

	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})

	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)

	s.schemes = append(s.schemes, scheme)

	s.schemeBuilder.Register(
		func(scheme *runtime.Scheme) error {
			gvs := []schema.GroupVersion{}
			for gv := range s.groupVersions {
				gvs = append(gvs, gv)
			}

			err := scheme.SetVersionPriority(gvs...)
			if err != nil {
				return fmt.Errorf("failed to set version priority: %w", err)
			}

			for _, gvr := range s.orderedGroupVersions {
				metav1.AddToGroupVersion(scheme, gvr)
			}

			return nil
		},
	)

	for i := range s.schemes {
		err := s.schemeBuilder.AddToScheme(s.schemes[i])
		if err != nil {
			return nil, fmt.Errorf("failed to add to scheme: %w", err)
		}
	}

	srv := genericserver.New(ctx,
		genericserver.WithGRPCServerFactory(kms.NewGRPCServerFactory()),
		genericserver.WithHTTPMuxFactory(s.newAPIGroupFactoryHandler()),
	)

	return srv, nil
}

// WithResourceAndHandler registers a resource and its handler with the server.
func (s *Server) WithResourceAndHandler(obj resource.Object, provider ResourceHandlerProvider) *Server {
	if s.storageProvider == nil {
		s.storageProvider = make(map[schema.GroupVersionResource]*ResourceProvider)
	}

	s.schemeBuilder.Register(resource.AddToScheme(obj))

	gvr := obj.GetGroupVersionResource()
	//nolint:varnamelen
	gv := gvr.GroupVersion()

	if s.groupVersions == nil {
		s.groupVersions = map[schema.GroupVersion]bool{}
	}

	if _, found := s.groupVersions[gv]; !found {
		s.groupVersions[gv] = true
		s.orderedGroupVersions = append(s.orderedGroupVersions, gv)
	}

	if _, found := s.storageProvider[gvr]; !found {
		s.storageProvider[gvr] = &ResourceProvider{Provider: provider}
	}

	return s
}

func (s *Server) newAPIGroupFactoryHandler() genericserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		// Discovery endpoint for API groups
		mux.HandleFunc("/apis", s.newGroupDiscoveryHandler())
		mux.HandleFunc("/apis/", s.newGroupDiscoveryHandler())

		// All API groups and versions in prioritized order
		//nolint:varnamelen
		for _, gv := range s.schemes[0].PrioritizedVersionsAllGroups() {
			storageProviders := map[string]rest.Storage{}

			// Find all storage providers for this group version
			for storageGVR, provider := range s.storageProvider {
				if storageGVR.GroupVersion() != gv {
					continue
				}

				resourceProviderFunc := provider.Provider
				if resourceProviderFunc == nil {
					return nil
				}

				resourceProvider, err := resourceProviderFunc(s.schemes[0], nil)
				if err != nil {
					return err
				}

				storageProviders[storageGVR.Resource] = resourceProvider
			}

			prefix := "/apis/" + gv.Group + "/" + gv.Version

			// Create a handler for each group version
			mux.Handle(prefix+"/", genericserver.NewAPIVersionHandler(
				gv,
				storageProviders,
				serializer.NewCodecFactory(s.schemes[0]),
				1*time.Minute,
				true,
			))

			// Register the group version discovery handler
			mux.HandleFunc(prefix, s.newGroupVersionDiscoveryHandler(gv))
		}

		return nil
	}
}

func (s *Server) newGroupDiscoveryHandler() http.HandlerFunc {
	return func(res http.ResponseWriter, _ *http.Request) {
		groups := map[string]metav1.APIGroup{}
		for group := range s.storageProvider {
			if _, found := groups[group.Group]; found {
				continue
			}

			gvfd := []metav1.GroupVersionForDiscovery{}
			gvs := s.schemes[0].PrioritizedVersionsForGroup(group.Group)

			for _, gv := range gvs {
				gvfd = append(gvfd, metav1.GroupVersionForDiscovery{
					GroupVersion: gv.String(),
					Version:      gv.Version,
				})
			}

			groups[group.Group] = metav1.APIGroup{
				Name:     group.Group,
				Versions: gvfd,
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: gvs[0].String(),
					Version:      gvs[0].Version,
				},
			}
		}

		apiGroupList := &metav1.APIGroupList{
			Groups: slices.Collect(maps.Values(groups)),
		}

		res.Header().Set("Content-Type", "application/json")

		err := encoding.NewKubeJSONEncoder(res).Encode(apiGroupList)
		if err != nil {
			// logger.Error("Failed to encode API group", zap.Error(err))
			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
		}
	}
}

func (s *Server) newGroupVersionDiscoveryHandler(groupVersion schema.GroupVersion) http.HandlerFunc {
	return func(res http.ResponseWriter, _ *http.Request) {
		resources := []metav1.APIResource{}

		for gvr, provider := range s.storageProvider {
			if gvr.GroupVersion() != groupVersion {
				continue
			}

			if provider == nil {
				http.Error(res, "Resource not supported", http.StatusNotFound)

				return
			}

			resource := gvr.Resource

			// Skip subresources.
			if strings.Contains(resource, "/") {
				continue
			}

			storage, err := provider.Provider(s.schemes[0], nil)
			if err != nil {
				http.Error(res, "Failed to get resource storage", http.StatusInternalServerError)

				return
			}

			namespaced := false
			if scoper, ok := storage.(rest.Scoper); ok {
				namespaced = scoper.NamespaceScoped()
			}

			shortNames := []string{}
			obj := storage.New()

			if shortNamesProvider, ok := obj.(rest.ShortNamesProvider); ok {
				shortNames = shortNamesProvider.ShortNames()
			}

			resources = append(resources, metav1.APIResource{
				Name:       resource,
				Namespaced: namespaced,
				Kind:       reflect.ValueOf(obj).Elem().Type().Name(),
				ShortNames: shortNames,
				Verbs:      getVerbs(storage),
			})
		}

		resourceList := &metav1.APIResourceList{
			GroupVersion: groupVersion.String(),
			APIResources: resources,
		}

		res.Header().Set("Content-Type", "application/json")

		err := encoding.NewKubeJSONEncoder(res).Encode(resourceList)
		if err != nil {
			// logger.Error("Failed to encode API resource list", zap.Error(err))
			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
		}
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
