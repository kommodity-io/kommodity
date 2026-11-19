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
		&armpolicy.ClientOptions{Transport: transport},
	)
	require.NoError(t, err)

	return pipeline
}

// capturedARMRequest records what a test server received, for assertions
// after the client call returns.
type capturedARMRequest struct {
	body    []byte
	ifMatch string
}

// newCapturingLBServer starts an httptest TLS server that records the next
// PUT's body and If-Match header and answers 200 with a provisioning
// response, mirroring ARM's shape.
func newCapturingLBServer(t *testing.T, captured *capturedARMRequest) *httptest.Server {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var readErr error

		captured.body, readErr = io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("reading request body: %v", readErr)
		}

		captured.ifMatch = request.Header.Get("If-Match")

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"properties": {"provisioningState": "Succeeded"}}`))
	}))
	t.Cleanup(server.Close)

	return server
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

	captured := &capturedARMRequest{}
	server := newCapturingLBServer(t, captured)
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

	decodeErr := json.Unmarshal(captured.body, &decoded)
	require.NoError(t, decodeErr, "PUT body must be a JSON object, got: %s", string(captured.body))

	probes, probesOK := decoded["properties"].(map[string]any)["probes"].([]any)
	require.True(t, probesOK, "PUT body must carry properties.probes")

	probe, probeOK := probes[0].(map[string]any)
	require.True(t, probeOK)
	assert.Equal(t, "HTTPSProbe", probe["name"])

	probeProps, propsOK := probe["properties"].(map[string]any)
	require.True(t, propsOK)
	assert.Equal(t, "Tcp", probeProps["protocol"])
	assert.InDelta(t, 6443, probeProps["port"], 0.01)

	// The If-Match precondition must survive to the wire.
	assert.Equal(t, `W/"test-etag"`, captured.ifMatch)
}
