// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	postgresDefaultPort = "5432"
	startupTimeout      = 10 * time.Second
	pollInterval        = 5 * time.Second
	writeTimeout        = 15 * time.Second
	filePermission      = 0o600
)

var (
	errRepoRootNotFound = errors.New("repo root not found")
	errContainerNil     = errors.New("container is nil")
)

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres     tc.Container
	Kommodity    tc.Container
	KommodityCfg *rest.Config
	Network      *tc.DockerNetwork
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
			Env: map[string]string{
				"KOMMODITY_DB_URI": "postgres://kommodity:kommodity@postgres:" +
					postgresDefaultPort + "/kommodity?sslmode=disable",
				"KOMMODITY_INSECURE_DISABLE_AUTHENTICATION": "true",
				"KOMMODITY_INFRASTRUCTURE_PROVIDERS":        "kubevirt,scaleway,azure",
				"KOMMODITY_KINE_URI":                        "unix:///tmp/kine.sock",
			},
			WaitingFor: wait.ForHTTP("/healthz").WithPort("5000/tcp").WithStartupTimeout(startupTimeout),
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

// WaitForK8sResource waits for a Kubernetes resource to be created that matches the given criteria.
//
//nolint:cyclop // Cyclomatic complexity is acceptable for this function.
func WaitForK8sResource(config *rest.Config, namespace string, nameContains string, group string,
	version string, kind string, fieldPath string, fieldValue string, timeout time.Duration) error {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: kind,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		log.Printf("Waiting for resource %s/%s/%s in namespace %s (name contains: %q, field %q=%q)",
			group, version, kind, namespace, nameContains, fieldPath, fieldValue)

		list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list resources: %w", err)
		}

		for _, item := range list.Items {
			if nameContains != "" && !strings.Contains(item.GetName(), nameContains) {
				continue
			}

			if fieldPath != "" {
				parts := strings.Split(fieldPath, ".")

				value, found, err := unstructured.NestedString(item.Object, parts...)
				if err != nil || !found || value != fieldValue {
					continue
				}
			}

			if fieldValue != "" && fieldPath == "" {
				continue
			}

			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("resource %s/%s/%s not found in namespace %s within timeout (name contains: %q, field %q=%q): %w",
			group, version, kind, namespace, nameContains, fieldPath, fieldValue, err)
	}

	log.Printf("Resource %s/%s/%s found in namespace %s (name contains: %q, field %q=%q)",
		group, version, kind, namespace, nameContains, fieldPath, fieldValue)

	return nil
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
