package helpers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHcloudAPI is a minimal hcloud API stub that serves one resource per type
// labeled for cluster "test" and records every DELETE it receives.
type fakeHcloudAPI struct {
	mu             sync.Mutex
	deleted        []string
	labelSelectors []string
	failDeletes    map[string]bool
}

func (f *fakeHcloudAPI) handler() http.Handler {
	mux := http.NewServeMux()
	list := func(path string, body string) {
		mux.HandleFunc("GET "+path, func(writer http.ResponseWriter, request *http.Request) {
			f.mu.Lock()
			f.labelSelectors = append(f.labelSelectors, request.URL.Query().Get("label_selector"))
			f.mu.Unlock()

			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(body))
		})
	}
	del := func(path string, body string) {
		mux.HandleFunc("DELETE "+path, func(writer http.ResponseWriter, request *http.Request) {
			f.mu.Lock()
			f.deleted = append(f.deleted, request.URL.Path)
			fail := f.failDeletes[request.URL.Path]
			f.mu.Unlock()

			writer.Header().Set("Content-Type", "application/json")

			if fail {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`{"error":{"code":"not_found","message":"gone"}}`))

				return
			}

			_, _ = writer.Write([]byte(body))
		})
	}

	list("/servers", `{"servers":[{"id":1,"name":"cp-1"}]}`)
	del("/servers/1", `{"action":{"id":10,"status":"running","progress":0}}`)
	list("/load_balancers", `{"load_balancers":[{"id":2,"name":"lb-1"}]}`)
	del("/load_balancers/2", `{}`)
	list("/placement_groups", `{"placement_groups":[{"id":3,"name":"pg-1"}]}`)
	del("/placement_groups/3", `{}`)
	list("/networks", `{"networks":[{"id":4,"name":"net-1"}]}`)
	del("/networks/4", `{}`)

	return mux
}

func newFakeHcloud(t *testing.T, failDeletes map[string]bool) (*fakeHcloudAPI, *hcloud.Client) {
	t.Helper()

	fake := &fakeHcloudAPI{failDeletes: failDeletes}
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)

	client := hcloud.NewClient(hcloud.WithEndpoint(server.URL), hcloud.WithToken("dummy"))

	return fake, client
}

func TestCleanupHetznerClusterResourcesDeletesAllLabeledResources(t *testing.T) {
	t.Parallel()

	fake, client := newFakeHcloud(t, nil)

	helpers.CleanupHetznerClusterResourcesWithClient(context.Background(), client, "test")

	// Servers first, networks last: a network cannot go while servers use it.
	require.Equal(t,
		[]string{"/servers/1", "/load_balancers/2", "/placement_groups/3", "/networks/4"},
		fake.deleted)

	for _, selector := range fake.labelSelectors {
		assert.Equal(t, "caph-cluster-test=owned", selector)
	}
}

func TestCleanupHetznerClusterResourcesContinuesPastDeleteErrors(t *testing.T) {
	t.Parallel()

	fake, client := newFakeHcloud(t, map[string]bool{"/servers/1": true})

	helpers.CleanupHetznerClusterResourcesWithClient(context.Background(), client, "test")

	// The failed server delete must not stop the remaining resource types.
	require.Equal(t,
		[]string{"/servers/1", "/load_balancers/2", "/placement_groups/3", "/networks/4"},
		fake.deleted)
}
