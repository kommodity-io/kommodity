//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCredential is a static azcore.TokenCredential so the armClient pipeline
// can be built hermetically: GetToken never touches the network.
type testCredential struct{}

func (testCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newTestPipeline builds the same ARM pipeline the production client uses,
// pointed at the given (httptest TLS) endpoint. The transport skips TLS
// verification so the pipeline accepts the server's self-signed certificate.
func newTestPipeline(t *testing.T) azruntime.Pipeline {
	t.Helper()

	transport := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only, self-signed httptest cert
		},
	}

	pipeline, err := armruntime.NewPipeline(
		armModuleName, armModuleVersion, testCredential{}, azruntime.PipelineOptions{},
		&armpolicy.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}},
	)
	require.NoError(t, err)

	return pipeline
}

// TestArmClientPutByteSliceBodyNotDoubleMarshaled is a regression test for the
// LB probe fixer's PUT path: the fixer passes an already-marshaled JSON body
// as []byte. Before the fix, armClient.put fed that []byte to
// runtime.MarshalAsJSON, which json.Marshal's it — base64-encoding the whole
// LB into a JSON string. ARM then received a string instead of the resource
// object and answered HTTP 400 on every PUT. This test exercises the real
// armClient against an httptest server and asserts the wire body is the LB
// object itself, with the If-Match header preserved.
func TestArmClientPutByteSliceBodyNotDoubleMarshaled(t *testing.T) {
	t.Parallel()

	lbBody := []byte(`{
		"etag": "W/\"test-etag\"",
		"properties": {
			"probes": [
				{
					"name": "HTTPSProbe",
					"properties": {"protocol": "Tcp", "port": 6443}
				}
			]
		}
	}`)

	var receivedBody []byte

	var receivedIfMatch string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var readErr error

		receivedBody, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("reading request body: %v", readErr)
		}

		receivedIfMatch = r.Header.Get("If-Match")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"properties": {"provisioningState": "Succeeded"}}`))
	}))
	defer server.Close()

	client := &armClient{endpoint: server.URL, pipeline: newTestPipeline(t)}

	status, body, _, err := client.Put(
		context.Background(),
		"/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb",
		"2023-05-01",
		`W/"test-etag"`,
		lbBody,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, body)

	// The wire body must be the LB object itself, not a base64 string
	// wrapping it. Decoding it as a JSON object and finding the probe by name
	// proves the object shape survived the transport.
	var decoded map[string]any

	decodeErr := json.Unmarshal(receivedBody, &decoded)
	require.NoError(t, decodeErr, "PUT body must be a JSON object, got: %s", string(receivedBody))

	probes, ok := decoded["properties"].(map[string]any)["probes"].([]any)
	require.True(t, ok, "PUT body must carry properties.probes")

	probe, ok := probes[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "HTTPSProbe", probe["name"])

	probeProps := probe["properties"].(map[string]any)
	assert.Equal(t, "Tcp", probeProps["protocol"])
	assert.Equal(t, float64(6443), probeProps["port"])

	// The If-Match precondition must survive to the wire.
	assert.Equal(t, `W/"test-etag"`, receivedIfMatch)
}
