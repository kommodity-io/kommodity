// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

const (
	postgresDefaultPort = "5432"
	startupTimeout      = 10 * time.Second
	pollInterval        = 5 * time.Second
	writeTimeout        = 15 * time.Second
	filePermission      = 0o600
	kindClusterName     = "kommodity-kubevirt-test"
	scalewayTestSKU     = "DEV1-S"
)

// InfraClusterNamespace is the namespace in the kind cluster where KubeVirt VMs are deployed.
const InfraClusterNamespace = "kubevirt-test-ns"

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres     tc.Container
	Kommodity    tc.Container
	KommodityCfg *rest.Config
	Network      *tc.DockerNetwork
}

// Infrastructure defines provider-specific Helm value overrides for cluster chart installation.
type Infrastructure interface {
	ValuesFile() string
	Overrides() map[string]any
}

// ValuesFile returns the Helm values file for Scaleway.
func (s ScalewayInfra) ValuesFile() string { return scalewayValuesFile }

// Overrides returns the Helm value overrides for Scaleway testing.
func (s ScalewayInfra) Overrides() map[string]any {
	return map[string]any{
		"kommodity.nodepools.default.sku":     scalewayTestSKU,
		"kommodity.controlplane.sku":          scalewayTestSKU,
		"kommodity.provider.config.projectID": s.ProjectID,
		"kommodity.network.ipv4.nodeCIDR":     nil,
	}
}

// SetupContainers initializes and starts the necessary containers for testing.
func SetupContainers() (TestEnvironment, error) {
	ctx := context.Background()

	// Create network
	newNetwork, err := network.New(ctx)
	if err != nil {
		return TestEnvironment{}, fmt.Errorf("failed to create network: %w", err)
	}

	networkName := newNetwork.Name

	// Start Postgres
	postgres, err := startPostgresContainer(ctx, networkName)
	if err != nil {
		return TestEnvironment{}, fmt.Errorf("failed to start Postgres container: %w", err)
	}

	// Start Kommodityt API server
	kommodity, kommodityCfg, err := startKommodityContainer(ctx, networkName)
	if err != nil {
		return TestEnvironment{}, fmt.Errorf("failed to start Kommodity container: %w", err)
	}

	env := TestEnvironment{
		Postgres:     postgres,
		Kommodity:    kommodity,
		KommodityCfg: kommodityCfg,
		Network:      newNetwork,
	}

	return env, nil
}

func startPostgresContainer(ctx context.Context, networkName string) (tc.Container, error) {
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
		return nil, fmt.Errorf("failed to create postgres container: %w", err)
	}

	return postgres, nil
}

func startKommodityContainer(ctx context.Context, networkName string) (tc.Container, *rest.Config, error) {
	kommodity, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:        "kommodity:latest",
			Networks:     []string{networkName},
			ExposedPorts: []string{"5000/tcp"},
			ExtraHosts:   []string{"host.docker.internal:host-gateway"},
			Env: map[string]string{
				"KOMMODITY_DB_URI": "postgres://kommodity:kommodity@postgres:" +
					postgresDefaultPort + "/kommodity?sslmode=disable",
				"KOMMODITY_INSECURE_DISABLE_AUTHENTICATION": "true",
				"KOMMODITY_INFRASTRUCTURE_PROVIDERS":        "kubevirt,scaleway",
				"KOMMODITY_KINE_URI":                        "unix:///tmp/kine.sock",
			},
			WaitingFor: wait.ForHTTP("/readyz").WithPort("5000/tcp").WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start Kommodity container: %w", err)
	}

	kommodityHost, err := kommodity.Host(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get host where Kommodity container port is exposed: %w", err)
	}

	kommodityPort, err := kommodity.MappedPort(ctx, "5000")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get externally mapped port for Kommodity container port: %w", err)
	}

	kommodityCfg := &rest.Config{
		Host: "http://" + net.JoinHostPort(kommodityHost, kommodityPort.Port()),
	}

	return kommodity, kommodityCfg, nil
}

// FindRepoRoot returns the repository root directory.
func FindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	for {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}

		dir = parent
	}

	return "", fmt.Errorf("%w: searched from %s", errRepoRootNotFound, dir)
}

// Teardown stops and removes the containers and network used in the test environment.
func (e TestEnvironment) Teardown() {
	ctx := context.Background()

	_ = e.Postgres.Terminate(ctx)
	_ = e.Kommodity.Terminate(ctx)
	_ = e.Network.Remove(ctx)
}

// WriteKommodityLogsToFile retrieves the logs from the Kommodity container and writes them to the specified file.
func WriteKommodityLogsToFile(container tc.Container, filePath string) error {
	if container == nil {
		return errContainerNil
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()

	logsReader, err := container.Logs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Kommodity container logs: %w", err)
	}

	defer func() { _ = logsReader.Close() }()

	data, err := io.ReadAll(logsReader)
	if err != nil {
		return fmt.Errorf("failed to read Kommodity logs: %w", err)
	}

	err = os.WriteFile(filePath, data, filePermission)
	if err != nil {
		return fmt.Errorf("failed to write Kommodity logs to file: %w", err)
	}

	return nil
}

// setNestedValue sets a value at a dot-notation path in a nested map.
func setNestedValue(values map[string]any, path string, value any) error {
	err := unstructured.SetNestedField(values, value, strings.Split(path, ".")...)
	if err != nil {
		return fmt.Errorf("failed to set nested value at path %q: %w", path, err)
	}

	return nil
}

// getNestedString reads a string value at a dot-notation path, returning empty string if not found.
func getNestedString(values map[string]any, path string) (string, error) {
	val, found, err := unstructured.NestedString(values, strings.Split(path, ".")...)
	if !found || err != nil {
		return "", fmt.Errorf("failed to get nested string at path %q: %w", path, err)
	}

	return val, nil
}
