// Package helpers provides utilities for setting up test environments with containers.
package helpers

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	KommodityK8s  *kubernetes.Clientset
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
	kommodity, kommodityClient := startKommodityContainer(ctx, networkName)

	// Start K3s cluster
	k3sContainer, kubeconfig := startK3sContainer(ctx, newNetwork)
	ensureK3sNamespace(ctx, kubeconfig, "kubevirt-cluster-namespace")

	kommodityHost, _ := kommodity.Host(ctx)
	kommodityPort, _ := kommodity.MappedPort(ctx, "5000")

	env := TestEnvironment{
		Postgres:      postgres,
		Kommodity:     kommodity,
		KommodityK8s:  kommodityClient,
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

func startKommodityContainer(ctx context.Context, networkName string) (tc.Container, *kubernetes.Clientset) {
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

	kommodityClient, err := kubernetes.NewForConfig(&rest.Config{
		Host: "http://" + net.JoinHostPort(kommodityHost, kommodityPort.Port()),
	})
	if err != nil {
		panic(err)
	}

	return kommodity, kommodityClient
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
