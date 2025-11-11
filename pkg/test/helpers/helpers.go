// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresDefaultPort = "5432"
	startupTimeout     = 10 * time.Second
)

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres tc.Container
	App      tc.Container
	AppHost  string
	AppPort  string
}

// SetupContainers initializes and starts the necessary containers for testing.
func SetupContainers(t *testing.T) TestEnvironment {
	t.Helper()

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

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres: postgres,
		App:      kommodity,
		AppHost:  kommodityHost,
		AppPort:  kommodityPort.Port(),
	}
	
	return env
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
				"KOMMODITY_DB_URI":                          "postgres://kommodity:kommodity@postgres:" + postgresDefaultPort + "/kommodity?sslmode=disable",
				"KOMMODITY_INSECURE_DISABLE_AUTHENTICATION": "true",
				"KOMMODITY_INFRASTRUCTURE_PROVIDERS":        "kubevirt,scaleway,azure",
				"KOMMODITY_KINE_URI":                        "unix:///tmp/kine.sock",
			},
			WaitingFor: wait.ForHTTP("/healthz").WithPort("5000/tcp").WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	require.NoError(t, err)

	return kommodity
}
