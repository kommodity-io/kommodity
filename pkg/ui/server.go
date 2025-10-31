// Package ui implements the Kommodity web user interface server.
package ui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"

	api "github.com/kommodity-io/kommodity/pkg/ui/api"
)

//go:generate npm run build --prefix web/kommodity-ui
//go:embed web/kommodity-ui/dist
var webDist embed.FS

// NewHTTPMuxFactory creates a new HTTP mux factory for serving the Kommodity UI.
func NewHTTPMuxFactory(cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		sub, err := fs.Sub(webDist, "web/kommodity-ui/dist")
		if err != nil {
			return fmt.Errorf("failed to create sub filesystem for UI: %w", err)
		}

		handler := spaHandler(sub)
		mux.Handle("/ui/{clusterName}", handler)
		mux.Handle("/assets/", handler)
		mux.Handle("/public/", handler)
		mux.Handle("/static/", handler)

		mux.HandleFunc("GET /api/kubeconfig/{clusterName}", api.GetKubeConfig(cfg))

		return nil
	}
}

func spaHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

			return
		}

		path := filepath.Clean(strings.TrimPrefix(request.URL.Path, "/ui"))
		if isRoot(path) || isClusterPath(path) {
			path = "index.html"
		}

		path = strings.TrimPrefix(path, "/")

		file, err := dist.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(response, request)

				return
			}

			http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

			return
		}

		response.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))

		if strings.HasPrefix(path, "static/") {
			response.Header().Set("Cache-Control", "public, max-age=31536000")
		}

		stat, err := file.Stat()
		if err == nil && stat.Size() > 0 {
			response.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		}

		_, err = io.Copy(response, file)

		defer func() {
			_ = file.Close()
		}()

		if err != nil {
			http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

			return
		}
	})
}

func isRoot(path string) bool {
	return path == "/" || path == "" || path == "."
}

func isClusterPath(path string) bool {
	trimmed := strings.Trim(path, "/")

	return strings.Count(trimmed, "/") == 0 && !strings.Contains(trimmed, ".")
}
