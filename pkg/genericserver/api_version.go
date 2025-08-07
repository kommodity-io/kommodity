package genericserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kommodity-io/kommodity/pkg/apis/core/v1alpha1"
	"github.com/kommodity-io/kommodity/pkg/encoding"
	"github.com/kommodity-io/kommodity/pkg/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
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

// NewAPIVersionHandler creates a new APIVersionHandler.
func NewAPIVersionHandler(
	groupVersion schema.GroupVersion,
	storage map[string]rest.Storage,
	serializer runtime.NegotiatedSerializer,
	minRequestTimeout time.Duration,
	enableAPIResponseCompression bool,
) *APIVersionHandler {
	return &APIVersionHandler{
		groupVersion:                 groupVersion,
		storage:                      storage,
		serializer:                   serializer,
		minRequestTimeout:            minRequestTimeout,
		enableAPIResponseCompression: enableAPIResponseCompression,
	}
}

type RoutingParameters struct {
	groupVersion      schema.GroupVersion
	namespace         string
	resource          string
	maybeResourceName string
}

// ServeHTTP handles API requests.
func (h *APIVersionHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	res.Header().Set("Content-Type", "application/json")

	// Extract routing parameters from the request.
	params, err := extractRoutingParameters(req)
	if err != nil {
		http.Error(res, "Invalid request path", http.StatusBadRequest)
		return
	}

	// Fetch the storage for the requested resource.
	storage, ok := h.storage[params.resource]
	if !ok {
		http.Error(res, "Resource not found", http.StatusNotFound)
		return
	}

	// h.logger.Info("Handling request",
	// 	zap.String("url", req.URL.Path),
	// 	zap.String("method", req.Method),
	// 	zap.String("resource", params.resource),
	// 	zap.String("groupVersion", params.groupVersion.String()),
	// 	zap.String("namespace", params.namespace),
	// 	zap.String("maybeResourceName", params.maybeResourceName),
	// )

	ctx := req.Context()
	if params.namespace != "" {
		// If the request is namespace-scoped, set the namespace in the context.
		ctx = genericapirequest.WithNamespace(ctx, params.namespace)
	}

	// Handle the request
	obj, apiErr, statusCode := handleRequest(ctx, req, params, storage)
	if apiErr != nil {
		handleError(res, apiErr, statusCode)
		return
	}

	if obj != nil {
		scheme := runtime.NewScheme()
		if err := v1alpha1.SchemeBuilder().AddToScheme(scheme); err != nil {
			http.Error(res, fmt.Sprintf("failed to add serializer to scheme: %v", err), http.StatusInternalServerError)
			return
		}

		err := encoding.NewKubeJSONEncoder(res).EncodeWithScheme(obj, scheme)
		if err != nil {
			http.Error(res, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
	}

	res.WriteHeader(statusCode)
}

func handleRequest(
	ctx context.Context,
	req *http.Request,
	params *RoutingParameters,
	storage rest.Storage,
) (runtime.Object, error, int) {
	switch req.Method {
	case http.MethodGet:
		if params.maybeResourceName != "" {
			// Handle GET for a specific resource.
			getter, ok := storage.(rest.Getter)
			if !ok {
				return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
			}

			obj, apiErr := getter.Get(ctx, params.maybeResourceName, nil)
			if apiErr != nil {
				return nil, apiErr, http.StatusInternalServerError
			}

			if obj == nil {
				return nil, fmt.Errorf("resource not found"), http.StatusNotFound
			}
			return obj, nil, http.StatusOK
		} else {
			// Handle GET for a list of resources.
			lister, ok := storage.(rest.Lister)
			if !ok {
				return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
			}

			obj, apiErr := lister.List(ctx, nil)
			if apiErr != nil {
				return nil, apiErr, http.StatusInternalServerError
			}

			if obj == nil {
				return nil, fmt.Errorf("no resources found"), http.StatusNotFound
			}
			return obj, nil, http.StatusOK
		}

	case http.MethodPost:
		creater, ok := storage.(rest.Creater)
		if !ok {
			return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
		}

		obj := storage.New()
		if err := encoding.NewKubeJSONDecoder(req.Body).Decode(obj); err != nil {
			return nil, fmt.Errorf("failed to decode request body: %w", err), http.StatusBadRequest
		}

		validationObj := obj.(validation.Validatable)

		obj, apiErr := creater.Create(ctx, obj, validationObj.CreateValidation, nil)
		if apiErr != nil {
			return nil, apiErr, http.StatusInternalServerError
		}
		if obj == nil {
			return nil, fmt.Errorf("failed to create resource"), http.StatusInternalServerError
		}

		return obj, nil, http.StatusCreated

	case http.MethodPut, http.MethodPatch:
		updater, ok := storage.(rest.Updater)
		if !ok {
			return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
		}

		obj := storage.New()
		if err := encoding.NewKubeJSONDecoder(req.Body).Decode(obj); err != nil {
			return nil, fmt.Errorf("failed to decode request body: %w", err), http.StatusBadRequest
		}

		validationObj := obj.(validation.Validatable)
		updatedObject := obj.(rest.UpdatedObjectInfo)

		obj, _, apiErr := updater.Update(ctx, params.maybeResourceName, updatedObject,
			validationObj.CreateValidation, validationObj.UpdateValidation, false, nil)
		if apiErr != nil {
			return nil, apiErr, http.StatusInternalServerError
		}

		if obj == nil {
			return nil, fmt.Errorf("failed to update resource"), http.StatusInternalServerError
		}

		return obj, nil, http.StatusOK

	case http.MethodDelete:
		validationObj := storage.New().(validation.Validatable)

		if params.maybeResourceName != "" {
			// Handle DELETE for a specific resource.
			deleter, ok := storage.(rest.GracefulDeleter)
			if !ok {
				return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
			}

			var instant bool

			obj, instant, apiErr := deleter.Delete(ctx, params.maybeResourceName, validationObj.DeleteValidation, nil)
			if apiErr != nil {
				return nil, apiErr, http.StatusInternalServerError
			}

			if instant {
				return obj, nil, http.StatusAccepted
			} else {
				return obj, nil, http.StatusNoContent
			}
		} else {
			// Handle DELETE for a collection of resources.
			deleter, ok := storage.(rest.CollectionDeleter)
			if !ok {
				return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
			}

			obj, apiErr := deleter.DeleteCollection(ctx, validationObj.DeleteValidation, nil, nil)
			if apiErr != nil {
				return nil, apiErr, http.StatusInternalServerError
			}

			return obj, nil, http.StatusNoContent
		}
	}

	return nil, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed
}

func extractRoutingParameters(r *http.Request) (*RoutingParameters, error) {
	// Example path: /apis/<group>/<version>/namespaces/<namespace>/<resource>/<name>
	// Or:           /apis/<group>/<version>/<resource>/<name> (cluster-scoped)
	args := strings.Split(r.URL.Path, "/")

	// Remove empty segments from args
	var segments []string

	for _, segment := range args {
		if segment != "" {
			segments = append(segments, segment)
		}
	}

	// Must have at least: apis, group, version, resource
	if len(segments) < 4 {
		return nil, http.ErrNotSupported
	}

	group := segments[1]
	version := segments[2]

	var namespace, resource, name string

	if len(segments) >= 6 && segments[3] == "namespaces" {
		namespace = segments[4]
		resource = segments[5]

		if len(segments) >= 7 {
			name = segments[6]
		}
	} else {
		resource = segments[3]
		if len(segments) >= 5 {
			name = segments[4]
		}
	}

	return &RoutingParameters{
		groupVersion:      schema.GroupVersion{Group: group, Version: version},
		namespace:         namespace,
		resource:          resource,
		maybeResourceName: name,
	}, nil
}

func handleError(res http.ResponseWriter, err error, statusCode int) {
	res.WriteHeader(statusCode)

	jsonErr := map[string]string{"error": err.Error()}

	encodeErr := json.NewEncoder(res).Encode(jsonErr)
	if encodeErr != nil {
		http.Error(res, "Failed to encode error response", http.StatusInternalServerError)
		return
	}
}
