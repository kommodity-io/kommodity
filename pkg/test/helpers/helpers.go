// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8s_wait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	postgresDefaultPort  = "5432"
	startupTimeout       = 10 * time.Second
	kubevirtManifestsDir = "pkg/test/manifests/kubevirt"
	rancherK3sImage      = "rancher/k3s:v1.27.1-k3s1"
)

// TestEnvironment holds the containers and connection info for the test setup.
type TestEnvironment struct {
	Postgres      tc.Container
	Kommodity     tc.Container
	KommodityCfg  *rest.Config
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
	kommodity, kommodityCfg := startKommodityContainer(ctx, networkName)

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres:      postgres,
		Kommodity:     kommodity,
		KommodityCfg:  kommodityCfg,
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

func startKommodityContainer(ctx context.Context, networkName string) (tc.Container, *rest.Config) {
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

	kommodityHost, err := kommodity.Host(ctx)
	if err != nil {
		panic(err)
	}
	kommodityPort, err := kommodity.MappedPort(ctx, "5000")
	if err != nil {
		panic(err)
	}

	kommodityCfg := &rest.Config{
		Host: "http://" + net.JoinHostPort(kommodityHost, kommodityPort.Port()),
	}

	return kommodity, kommodityCfg
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

// RepoRoot returns the repository root directory.
func RepoRoot() string {
	return findRepoRoot()
}

// K8sClientFromRestConfig builds a Kubernetes client from a REST config.
func K8sClientFromRestConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(cfg)
}

// Teardown stops and removes the containers and network used in the test environment.
func (e TestEnvironment) Teardown() {
	ctx := context.Background()

	_ = e.Postgres.Terminate(ctx)
	_ = e.Kommodity.Terminate(ctx)
	_ = e.Network.Remove(ctx)
}

func WaitForResource(config *rest.Config, namespace string, nameContains string, group string, version string, kind string, fieldPath string, fieldValue string, timeout time.Duration) error {
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

func RestConfigFromKubeConfig(kubeconfig []byte) (*rest.Config, error) {
	return clientcmd.RESTConfigFromKubeConfig(kubeconfig)
}
