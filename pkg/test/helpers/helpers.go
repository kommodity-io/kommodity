// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresDefaultPort = "5432"
	startupTimeout      = 10 * time.Second
)

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres      tc.Container
	Kommodity     tc.Container
	K3s           *k3s.K3sContainer
	K3sKubeconfig []byte
	AppHost       string
	AppPort       string
	Network       *tc.DockerNetwork
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

	// Start K3s cluster
	k3sContainer, kubeconfig := startK3sContainer(ctx, newNetwork)

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres:      postgres,
		Kommodity:     kommodity,
		K3s:           k3sContainer,
		K3sKubeconfig: kubeconfig,
		AppHost:       kommodityHost,
		AppPort:       kommodityPort.Port(),
		Network:       newNetwork,
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

func startK3sContainer(ctx context.Context, net *tc.DockerNetwork) (*k3s.K3sContainer, []byte) {
	repoRoot := findRepoRoot()
	kubevirtOperator := filepath.Join(repoRoot, "pkg", "test", "manifests", "kubevirt", "kubevirt-operator.yaml")
	kubevirtCR := filepath.Join(repoRoot, "pkg", "test", "manifests", "kubevirt", "kubevirt-cr.yaml")

	k3sContainer, err := k3s.Run(ctx, "rancher/k3s:v1.27.1-k3s1",
		network.WithNetwork([]string{"k3s"}, net),
		k3s.WithManifest(kubevirtOperator),
		k3s.WithManifest(kubevirtCR),
	)
	if err != nil {
		panic(err)
	}

	kubeconfig, err := k3sContainer.GetKubeConfig(ctx)
	if err != nil {
		panic(err)
	}

	return k3sContainer, kubeconfig
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	panic("go.mod not found")
}

// Teardown stops and removes the containers and network used in the test environment.
func (e TestEnvironment) Teardown() {
	ctx := context.Background()

	_ = e.Postgres.Terminate(ctx)
	_ = e.Kommodity.Terminate(ctx)
	if e.K3s != nil {
		_ = e.K3s.Terminate(ctx)
	}
	_ = e.Network.Remove(ctx)
}
