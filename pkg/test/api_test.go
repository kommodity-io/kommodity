package test_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/test/apirequests"
	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // Test environment needs to be reused by all tests.
var env helpers.TestEnvironment

func TestMain(m *testing.M) {
	// --- Setup ---
	env = helpers.SetupContainers()

	// Run tests
	code := m.Run()

	// --- Teardown ---
	env.Teardown()

	os.Exit(code)
}

func TestAPIIntegration(t *testing.T) {
	t.Parallel()

	newRequest := apirequests.APIRequest{
		TestEnvironment:    env,
		Endpoint:           "/api",
		Type:               "GET",
		ExpectedStatusCode: 200,
		ExpectedResponseBody: `{
					"kind": "APIVersions",
					"versions": [
						"v1"
					],
					"serverAddressByClientCIDRs": [
						{
						"clientCIDR": "0.0.0.0/0",
						"serverAddress": ":8443"
						}
					]
					}`,
	}

	newRequest.RunRequest(t)
}

func TestCreateSecret(t *testing.T) {
	t.Parallel()

	// Default namespace needs to be created first

	namespacePayload := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": "default",
		},
	}
	namespaceBody, err := json.Marshal(namespacePayload)
	require.NoError(t, err)

	nsReq, err := http.NewRequest(http.MethodPost,
		"http://"+net.JoinHostPort(env.AppHost, env.AppPort)+"/api/v1/namespaces",
		bytes.NewReader(namespaceBody))
	require.NoError(t, err)
	nsReq.Header.Set("Content-Type", "application/json")

	nsResp, err := (&http.Client{}).Do(nsReq)
	require.NoError(t, err)
	defer func() {
		_ = nsResp.Body.Close()
	}()
	if nsResp.StatusCode != http.StatusCreated && nsResp.StatusCode != http.StatusConflict {
		t.Fatalf("unexpected namespace create status: %d", nsResp.StatusCode)
	}

	// Create secret with K3s kubeconfig (needed for Kubevirt tests)

	kubeconfig := base64.StdEncoding.EncodeToString(env.K3sKubeconfig)
	payload := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "k3s-credentials",
			"namespace": "default",
		},
		"data": map[string]any{
			"kubeconfig": kubeconfig,
		},
		"type": "Opaque",
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost,
		"http://"+net.JoinHostPort(env.AppHost, env.AppPort)+"/api/v1/namespaces/default/secrets",
		bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify the secret was created
	getReq, err := http.NewRequest(http.MethodGet,
		"http://"+net.JoinHostPort(env.AppHost, env.AppPort)+"/api/v1/namespaces/default/secrets/k3s-credentials", nil)
	require.NoError(t, err)

	getResp, err := (&http.Client{}).Do(getReq)
	require.NoError(t, err)
	defer func() {
		_ = getResp.Body.Close()
	}()

	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var secret struct {
		Data map[string]string `json:"data"`
		Type string            `json:"type"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&secret))
	require.Equal(t, "Opaque", secret.Type)
	require.Equal(t, kubeconfig, secret.Data["kubeconfig"])

	// Create a kubevirt-cluster-namespace for Kubevirt tests

	
}
