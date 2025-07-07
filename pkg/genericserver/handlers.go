package genericserver

import (
	"net/http"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/encoding"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

// newGroupDiscoveryHandler returns a new discovery handler for a specific API group.
func newGroupDiscoveryHandler(
	logger *zap.Logger,
	apiGroupInfo *genericapiserver.APIGroupInfo,
	groupName string,
) http.HandlerFunc {
	return func(res http.ResponseWriter, _ *http.Request) {
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

		err := encoding.NewKubeJSONEncoder(res).Encode(apiGroup)
		if err != nil {
			logger.Error("Failed to encode API group", zap.Error(err))
			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
		}
	}
}

// newGroupVersionDiscoveryHandler returns a new discovery handler for a specific API group version.
func newGroupVersionDiscoveryHandler(
	logger *zap.Logger,
	apiGroupInfo *genericapiserver.APIGroupInfo,
	groupVersion schema.GroupVersion,
) http.HandlerFunc {
	return func(res http.ResponseWriter, _ *http.Request) {
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

		err := encoding.NewKubeJSONEncoder(res).Encode(resourceList)
		if err != nil {
			logger.Error("Failed to encode API resource list", zap.Error(err))
			http.Error(res, encoding.ErrEncodingFailed.Error(), http.StatusInternalServerError)
		}
	}
}
