package test_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresDefaultPort = "5432"
)

//nolint:paralleltest // This test uses shared Docker resources; cannot run in parallel.
func TestAPIIntegration(t *testing.T) {
	ctx := context.Background()

	// Create network
	newNetwork, err := network.New(ctx)
	require.NoError(t, err)
	tc.CleanupNetwork(t, newNetwork)

	networkName := newNetwork.Name

	// Start Postgres
	postgres := startPostgresContainer(ctx, t, networkName)
	tc.CleanupContainer(t, postgres)

	// Start Kommodityt API server
	kommodity := startKommodityContainer(ctx, t, networkName)
	tc.CleanupContainer(t, kommodity)

	appHost, _ := kommodity.Host(ctx)
	appPort, _ := kommodity.MappedPort(ctx, "5000")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(appHost, appPort.Port())+"/api", nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() {
		_ = resp.Body.Close()
	}()

	require.Equal(t, 200, resp.StatusCode, "unexpected HTTP status code")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Parse JSON response
	var got map[string]interface{}

	err = json.Unmarshal(body, &got)
	require.NoError(t, err, "response is not valid JSON")

	expected := map[string]interface{}{
		"kind":     "APIVersions",
		"versions": []interface{}{"v1"},
		"serverAddressByClientCIDRs": []interface{}{
			map[string]interface{}{
				"clientCIDR":    "0.0.0.0/0",
				"serverAddress": ":8443",
			},
		},
	}

	require.Equal(t, expected, got, "unexpected API response body")
}

func startPostgresContainer(ctx context.Context, t *testing.T, networkName string) tc.Container {
	t.Helper()

	postgres, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:    "postgres:16",
			Networks: []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {"postgres"},
			},

			ExposedPorts: []string{postgresDefaultPort + "/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "kommodity",
				"POSTGRES_PASSWORD": "kommodity",
				"POSTGRES_DB":       "kommodity",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections"),
		},
		Started: true,
	})
	require.NoError(t, err)

	return postgres
}

func startKommodityContainer(ctx context.Context, t *testing.T, networkName string) tc.Container {
	t.Helper()

	kommodity, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:        "kommodity:latest",
			Networks:     []string{networkName},
			ExposedPorts: []string{"5000/tcp"},
			Env: map[string]string{
				"KOMMODITY_DB_URI": "postgres://kommodity:kommodity@postgres:" + postgresDefaultPort + "/kommodity?sslmode=disable",
				"KOMMODITY_INSECURE_DISABLE_AUTHENTICATION": "true",
				"KOMMODITY_INFRASTRUCTURE_PROVIDERS":        "kubevirt,scaleway,azure",
				"KOMMODITY_KINE_URI":                        "unix:///tmp/kine.sock",
			},
			WaitingFor: wait.ForHTTP("/healthz").WithPort("5000/tcp").WithStartupTimeout(10 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	return kommodity
}
