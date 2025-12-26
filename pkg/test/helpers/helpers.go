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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	// Start K3s cluster
	k3sContainer, kubeconfig := startK3sContainer(ctx, newNetwork)
	ensureK3sNamespace(ctx, kubeconfig, "kubevirt-cluster-namespace")

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres:      postgres,
		Kommodity:     kommodity,
		KommodityCfg:  kommodityCfg,
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

func startK3sContainer(ctx context.Context, net *tc.DockerNetwork) (*k3s.K3sContainer, []byte) {
	repoRoot := findRepoRoot()
	kubevirtOperator := filepath.Join(repoRoot, kubevirtManifestsDir, "kubevirt-operator.yaml")
	kubevirtCR := filepath.Join(repoRoot, kubevirtManifestsDir, "kubevirt-cr.yaml")

	k3sContainer, err := k3s.Run(ctx, rancherK3sImage,
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

	// Print the kubeconfig to a file for debugging purposes
	err = os.WriteFile(filepath.Join(findRepoRoot(), "k3s_kubeconfig.yaml"), kubeconfig, 0644)
	if err != nil {
		panic(err)
	}

	k3sCfg, err := RestConfigFromKubeConfig(kubeconfig)
	if err != nil {
		panic(err)
	}

	// Wait for Kubevirt to be ready (status.phase=Deployed)
	err = WaitForResource(k3sCfg, "kubevirt", "kubevirt", "kubevirt.io", "v1", "kubevirts", "status.phase", "Deployed", 10*time.Minute)
	if err != nil {
		panic(err)
	}

	return k3sContainer, kubeconfig
}

func ensureK3sNamespace(ctx context.Context, kubeconfig []byte, name string) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
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
	if e.K3s != nil {
		_ = e.K3s.Terminate(ctx)
	}
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
