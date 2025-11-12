// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresDefaultPort = "5432"
	startupTimeout      = 10 * time.Second
)

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres  tc.Container
	Kommodity tc.Container
	AppHost   string
	AppPort   string
	Network   *tc.DockerNetwork
}

// SetupContainers initializes and starts the necessary containers for testing.
func SetupContainers() TestEnvironment {
	ctx := context.Background()

	// Create network
	newNetwork, err := network.New(ctx)
	if err != nil {
		panic(err)
	}

	networkName := newNetwork.Name

	// Start Postgres
	postgres := startPostgresContainer(ctx, networkName)

	// Start Kommodityt API server
	kommodity := startKommodityContainer(ctx, networkName)

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres:  postgres,
		Kommodity: kommodity,
		AppHost:   kommodityHost,
		AppPort:   kommodityPort.Port(),
		Network:   newNetwork,
	}

	return env
}

func startPostgresContainer(ctx context.Context, networkName string) tc.Container {
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
	if err != nil {
		panic(err)
	}

	return postgres
}

func startKommodityContainer(ctx context.Context, networkName string) tc.Container {
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
	if err != nil {
		panic(err)
	}

	return kommodity
}

func (e TestEnvironment) Teardown() {
	ctx := context.Background()

	_ = e.Postgres.Terminate(ctx)
	_ = e.Kommodity.Terminate(ctx)
	_ = e.Network.Remove(ctx)
}
