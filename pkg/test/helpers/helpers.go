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
		return nil, err
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
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("go.mod not found in any parent directory of %s", dir)
}

// Teardown stops and removes the containers and network used in the test environment.
func (e TestEnvironment) Teardown() {
	ctx := context.Background()

	_ = e.Postgres.Terminate(ctx)
	_ = e.Kommodity.Terminate(ctx)
	_ = e.Network.Remove(ctx)
}

// WaitForK8sResource waits for a Kubernetes resource to be created that matches the given criteria.
func WaitForK8sResource(config *rest.Config, namespace string, nameContains string, group string, version string, kind string, fieldPath string, fieldValue string, timeout time.Duration) error {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %v", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: kind,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = k8s_wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		println(fmt.Sprintf("Waiting for resource %s/%s/%s in namespace %s (name contains: %q, field %q=%q)", group, version, kind, namespace, nameContains, fieldPath, fieldValue))
		list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
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
		return fmt.Errorf("resource %s/%s/%s not found in namespace %s within timeout (name contains: %q, field %q=%q): %v", group, version, kind, namespace, nameContains, fieldPath, fieldValue, err)
	}

	println(fmt.Sprintf("Resource %s/%s/%s found in namespace %s (name contains: %q, field %q=%q)", group, version, kind, namespace, nameContains, fieldPath, fieldValue))
	return nil
}

func WriteKommodityLogsToFile(container tc.Container, filePath string) error {
	if container == nil {
		return fmt.Errorf("container is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logsReader, err := container.Logs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Kommodity container logs: %w", err)
	}
	defer logsReader.Close()

	data, err := io.ReadAll(logsReader)
	if err != nil {
		return fmt.Errorf("failed to read Kommodity logs: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write Kommodity logs to file: %w", err)
	}

	return nil
}
