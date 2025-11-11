package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.uber.org/zap"
)

const (
	postgresDefaultPort = "5432"
)

func TestAPIIntegration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	ctx := context.Background()

	// Create network
	networkName := "kommodity-test-net"

	newNetwork, err := network.New(ctx)
	require.NoError(t, err)
	tc.CleanupNetwork(t, newNetwork)

	networkName = newNetwork.Name

	// Start Postgres
	logger.Info("Starting Postgres test container...")
	pg, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
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
	tc.CleanupContainer(t, pg)
	require.NoError(t, err)

	pgHost, _ := pg.Host(ctx)
	pgPort, _ := pg.MappedPort(ctx, postgresDefaultPort)
	logger.Info("Postgres container started", zap.String("host", pgHost), zap.String("port", pgPort.Port()))

	// Start your API server (built into a container)
	logger.Info("Starting Kommodity API test container...")
	kommodity, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:        "kommodity:latest",
			Networks:     []string{networkName},
			ExposedPorts: []string{"5123/tcp"},
			Env: map[string]string{
				"KOMMODITY_DB_URI": "postgres://kommodity:kommodity@postgres:" + postgresDefaultPort + "/kommodity?sslmode=disable",
				"KOMMODITY_PORT":   "5123",
				"KOMMODITY_INSECURE_DISABLE_AUTHENTICATION": "true",
				"KOMMODITY_DEVELOPMENT_MODE":                "true",
				"KOMMODITY_INFRASTRUCTURE_PROVIDERS":        "kubevirt,scaleway",
				"KOMMODITY_KINE_URI":                        "unix:///tmp/kine.sock",
			},
			WaitingFor: wait.ForHTTP("/healthz").WithPort("5123/tcp").WithStartupTimeout(20 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	tc.CleanupContainer(t, kommodity)

	if err != nil {
		logger.Error("Failed to start Kommodity API container", zap.Error(err))

		// Try to read container logs
		if logs, logErr := kommodity.Logs(ctx); logErr == nil {
			defer logs.Close()
			logger.Info("Dumping Kommodity container logs:")
			buf := new(bytes.Buffer)
			_, _ = io.Copy(buf, logs)
			fmt.Println(buf.String())
		} else {
			logger.Error("Failed to fetch container logs", zap.Error(logErr))
		}

		t.Fatal(err)
	}

	appHost, _ := kommodity.Host(ctx)
	appPort, _ := kommodity.MappedPort(ctx, "5123")
	logger.Info("Kommodity API container started", zap.String("host", appHost), zap.String("port", appPort.Port()))

	// Now query your API automatically
	logger.Info("Querying Kommodity API endpoint...")
	resp, err := http.Get(fmt.Sprintf("http://%s:%s/api", appHost, appPort.Port()))
	if err != nil {
		logger.Error("Failed to reach Kommodity API", zap.Error(err))
		t.Fatalf("failed to reach API: %v", err)
	}
	defer resp.Body.Close()

	logger.Info("Received response from Kommodity API", zap.Int("status", resp.StatusCode))
	if resp.StatusCode != 200 {
		logger.Error("Unexpected status code", zap.Int("status", resp.StatusCode))
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Body == nil {
		logger.Error("Response body is nil")
		t.Fatal("expected non-nil response body")
	}
}
