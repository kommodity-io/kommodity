package genericserver

import (
	"net/http"
	"time"

	"github.com/kommodity-io/kommodity/pkg/encoding"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
)

// APIVersionHandler handles requests for API resources.
type APIVersionHandler struct {
	groupVersion                 schema.GroupVersion
	storage                      map[string]rest.Storage
	serializer                   runtime.NegotiatedSerializer
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
