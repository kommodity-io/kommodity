package talosproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	//nolint:gosec // This is not a credential, it's a secret name suffix.
	kubeconfigSecretSuffix = "-kubeconfig"
	kubeconfigSecretKey    = "value"
	kubeconfigNamespace    = "default"
	// labelKeyValueParts is the expected number of parts when splitting a label by "=".
	labelKeyValueParts = 2
	// loopbackAddress is the loopback IP address used for local proxy connections.
	loopbackAddress = "127.0.0.1"
	// runningPodFieldSelector filters pod listings to only include running pods.
	runningPodFieldSelector = "status.phase=Running"
)

// Tunnel represents a port-forward connection to a talos-proxy pod in a workload cluster.
type Tunnel struct {
	mu          sync.Mutex
	clusterName string
	config      *config.TalosProxyConfig
	localPort   uint16
	stopChan    chan struct{}
	closed      bool
	podName     string
	activeConns atomic.Int64
	onIdle      func()
}

// TunnelDeps holds the dependencies needed to create a tunnel.
type TunnelDeps struct {
	ClusterName string
	Config      *config.TalosProxyConfig
	OnIdle      func()
}

// NewTunnel creates a new tunnel for a workload cluster.
func NewTunnel(deps TunnelDeps) *Tunnel {
	return &Tunnel{
		clusterName: deps.ClusterName,
		config:      deps.Config,
		stopChan:    make(chan struct{}),
		onIdle:      deps.OnIdle,
	}
}

// Establish sets up the port-forward to the talos-proxy pod.
func (t *Tunnel) Establish(ctx context.Context, kubeClient client.Client) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTunnelClosed
	}

	logger := logging.FromContext(ctx)

	restConfig, err := t.fetchRESTConfig(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to fetch REST config for cluster %s: %w", t.clusterName, err)
	}

	podName, err := t.findProxyPod(ctx, restConfig)
	if err != nil {
		return fmt.Errorf("failed to find talos-proxy pod in cluster %s: %w", t.clusterName, err)
	}

	t.podName = podName

	localPort, err := t.startPortForward(ctx, restConfig, podName)
	if err != nil {
		return fmt.Errorf("failed to start port-forward for cluster %s: %w", t.clusterName, err)
	}

	t.localPort = localPort

	logger.Info("Tunnel established",
		zap.String("cluster", t.clusterName),
		zap.String("pod", podName),
		zap.Uint16("localPort", localPort))

	return nil
}

// Dial returns a new TCP connection through the port-forward tunnel.
// The returned connection is wrapped with reference counting; closing it
// decrements the tunnel's active connection count.
func (t *Tunnel) Dial(ctx context.Context) (net.Conn, error) {
	t.mu.Lock()

	if t.closed {
		t.mu.Unlock()

		return nil, ErrTunnelClosed
	}

	if t.localPort == 0 {
		t.mu.Unlock()

		return nil, ErrTunnelNotReady
	}

	port := t.localPort
	t.mu.Unlock()

	dialer := net.Dialer{}

	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", loopbackAddress, port))
	if err != nil {
		return nil, fmt.Errorf("failed to dial tunnel for cluster %s: %w", t.clusterName, err)
	}

	t.AcquireConn()

	return &trackedConn{Conn: conn, tunnel: t}, nil
}

// AcquireConn increments the active connection count.
func (t *Tunnel) AcquireConn() {
	t.activeConns.Add(1)
}

// ReleaseConn decrements the active connection count. When the count reaches
// zero, the onIdle callback is invoked (if set). The callback is called without
// holding the tunnel mutex to avoid deadlock with the pool lock.
func (t *Tunnel) ReleaseConn() {
	if t.activeConns.Add(-1) == 0 && t.onIdle != nil {
		t.onIdle()
	}
}

// ActiveConns returns the current number of active connections using this tunnel.
func (t *Tunnel) ActiveConns() int64 {
	return t.activeConns.Load()
}

// IsClosed returns whether the tunnel has been closed.
func (t *Tunnel) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.closed
}

// Close shuts down the port-forward tunnel.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true
	close(t.stopChan)

	return nil
}

// monitorPortForward watches for the port-forward goroutine to exit and marks
// the tunnel as closed. This enables the connect handler to detect stale tunnels
// and create fresh ones when the talos-proxy pod is evicted or rescheduled.
func (t *Tunnel) monitorPortForward(errChan <-chan error, logger *zap.Logger) {
	err := <-errChan

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	t.closed = true
	close(t.stopChan)

	logger.Warn("Port-forward terminated unexpectedly",
		zap.String("cluster", t.clusterName),
		zap.String("pod", t.podName),
		zap.Error(err))
}

func (t *Tunnel) fetchRESTConfig(ctx context.Context, kubeClient client.Client) (*rest.Config, error) {
	secret := &corev1.Secret{}
	secretName := t.clusterName + kubeconfigSecretSuffix

	err := kubeClient.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: kubeconfigNamespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %s: %w", secretName, err)
	}

	kubeConfigBytes, ok := secret.Data[kubeconfigSecretKey]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrKubeconfigNotFound, secretName)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	return restConfig, nil
}

func (t *Tunnel) findProxyPod(ctx context.Context, restConfig *rest.Config) (string, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	labelParts := strings.SplitN(t.config.ProxyLabel, "=", labelKeyValueParts)
	if len(labelParts) != labelKeyValueParts {
		return "", fmt.Errorf("%w: %s", ErrInvalidProxyLabel, t.config.ProxyLabel)
	}

	pods, err := clientset.CoreV1().Pods(t.config.ProxyNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: t.config.ProxyLabel,
		FieldSelector: runningPodFieldSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list talos-proxy pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("%w in namespace %s with label %s",
			ErrProxyPodNotFound, t.config.ProxyNamespace, t.config.ProxyLabel)
	}

	return pods.Items[0].Name, nil
}

func (t *Tunnel) startPortForward(
	ctx context.Context,
	restConfig *rest.Config,
	podName string,
) (uint16, error) {
	forwarder, err := t.createPortForwarder(restConfig, podName)
	if err != nil {
		return 0, err
	}

	return t.waitForPortForward(ctx, forwarder, podName)
}

func (t *Tunnel) createPortForwarder(
	restConfig *rest.Config,
	podName string,
) (*portforward.PortForwarder, error) {
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward",
		t.config.ProxyNamespace, podName)
	serverURL := restConfig.Host + path

	spdyDialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		http.MethodPost,
		parseURL(serverURL),
	)

	// Use port 0 to let the system assign a local port
	ports := []string{fmt.Sprintf("0:%d", t.config.ProxyPort)}
	readyChan := make(chan struct{})

	forwarder, err := portforward.New(
		spdyDialer,
		ports,
		t.stopChan,
		readyChan,
		nil, // stdout
		nil, // stderr
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	return forwarder, nil
}

func (t *Tunnel) waitForPortForward(
	ctx context.Context,
	forwarder *portforward.PortForwarder,
	podName string,
) (uint16, error) {
	logger := logging.FromContext(ctx)
	errChan := make(chan error, 1)

	go func() {
		errChan <- forwarder.ForwardPorts()
	}()

	select {
	case <-forwarder.Ready:
		// Port-forward is ready
	case err := <-errChan:
		return 0, fmt.Errorf("port-forward failed: %w", err)
	case <-ctx.Done():
		return 0, fmt.Errorf("context cancelled while waiting for port-forward: %w", ctx.Err())
	}

	// Monitor port-forward health — marks tunnel as closed if port-forward exits unexpectedly.
	// This enables early detection of stale tunnels when the talos-proxy pod is evicted or rescheduled.
	go t.monitorPortForward(errChan, logger)

	forwardedPorts, err := forwarder.GetPorts()
	if err != nil {
		return 0, fmt.Errorf("failed to get forwarded ports: %w", err)
	}

	if len(forwardedPorts) == 0 {
		return 0, ErrNoForwardedPorts
	}

	localPort := forwardedPorts[0].Local

	logger.Info("Port-forward started",
		zap.String("cluster", t.clusterName),
		zap.String("pod", podName),
		zap.Uint16("localPort", localPort),
		zap.Int("remotePort", t.config.ProxyPort))

	return localPort, nil
}
