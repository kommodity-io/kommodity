package talosproxy

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	// loopbackAddress is the loopback IP address used for local proxy connections.
	loopbackAddress = "127.0.0.1"
	// runningPodFieldSelector filters pod listings to only include running pods.
	runningPodFieldSelector = "status.phase=Running"
)

// Tunnel represents a port-forward connection to a talos-cluster-proxy pod in a workload cluster.
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

// Establish sets up the port-forward to the talos-cluster-proxy pod.
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

	target, err := t.resolveServiceTarget(ctx, restConfig)
	if err != nil {
		return fmt.Errorf("failed to resolve talos-cluster-proxy service in cluster %s: %w", t.clusterName, err)
	}

	t.podName = target.podName

	localPort, err := t.startPortForward(ctx, restConfig, target)
	if err != nil {
		return fmt.Errorf("failed to start port-forward for cluster %s: %w", t.clusterName, err)
	}

	t.localPort = localPort

	logger.Info("Tunnel established",
		zap.String("cluster", t.clusterName),
		zap.String("pod", target.podName),
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

	t.markClosedLocked()

	return nil
}

// markClosedLocked marks the tunnel as closed and signals the port-forward
// goroutine to exit by closing stopChan. Idempotent. Caller must hold t.mu.
func (t *Tunnel) markClosedLocked() {
	if t.closed {
		return
	}

	t.closed = true
	close(t.stopChan)
}

// monitorPortForward watches for the port-forward goroutine to exit and marks
// the tunnel as closed. This enables the connect handler to detect stale tunnels
// and create fresh ones when the talos-cluster-proxy pod is evicted or rescheduled.
func (t *Tunnel) monitorPortForward(errChan <-chan error, logger *zap.Logger) {
	err := <-errChan

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	t.markClosedLocked()

	if err == nil {
		logger.Info("Port-forward terminated cleanly",
			zap.String("cluster", t.clusterName),
			zap.String("pod", t.podName))

		return
	}

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

// serviceTarget is the resolved port-forward target for a Service: a concrete pod
// matching the service selector and the numeric container port for the service's
// first port (named target ports are resolved against the pod's container spec).
type serviceTarget struct {
	podName    string
	targetPort int32
}

func (t *Tunnel) resolveServiceTarget(ctx context.Context, restConfig *rest.Config) (*serviceTarget, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	svc, err := clientset.CoreV1().Services(t.config.ProxyNamespace).
		Get(ctx, t.config.ProxyServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: %s/%s: %w",
			ErrProxyServiceNotFound, t.config.ProxyNamespace, t.config.ProxyServiceName, err)
	}

	if len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("%w: %s/%s",
			ErrProxyServiceNoSelector, t.config.ProxyNamespace, t.config.ProxyServiceName)
	}

	if len(svc.Spec.Ports) == 0 {
		return nil, fmt.Errorf("%w: %s/%s",
			ErrProxyServiceNoPorts, t.config.ProxyNamespace, t.config.ProxyServiceName)
	}

	selector := labels.SelectorFromSet(svc.Spec.Selector).String()

	pods, err := clientset.CoreV1().Pods(t.config.ProxyNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		FieldSelector: runningPodFieldSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list talos-cluster-proxy pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("%w in namespace %s with selector %s",
			ErrProxyPodNotFound, t.config.ProxyNamespace, selector)
	}

	// Random pick spreads load across pods on tunnel rebuild (idle timeout, pod eviction).
	//nolint:gosec // Pod selection is load balancing, not a security boundary.
	pod := pods.Items[rand.IntN(len(pods.Items))]

	port, err := resolveTargetPort(svc.Spec.Ports[0], pod)
	if err != nil {
		return nil, err
	}

	return &serviceTarget{podName: pod.Name, targetPort: port}, nil
}

// resolveTargetPort resolves a service ServicePort target to a numeric container
// port on the given pod. Numeric target ports pass through; named target ports
// are looked up in the pod's container port specs.
func resolveTargetPort(svcPort corev1.ServicePort, pod corev1.Pod) (int32, error) {
	if svcPort.TargetPort.Type == intstr.Int {
		value := svcPort.TargetPort.IntVal
		if value == 0 {
			return svcPort.Port, nil
		}

		return value, nil
	}

	name := svcPort.TargetPort.StrVal

	for _, container := range pod.Spec.Containers {
		for _, cp := range container.Ports {
			if cp.Name == name {
				return cp.ContainerPort, nil
			}
		}
	}

	return 0, fmt.Errorf("%w: port %q on pod %s/%s",
		ErrTargetPortNotResolvable, name, pod.Namespace, pod.Name)
}

func (t *Tunnel) startPortForward(
	ctx context.Context,
	restConfig *rest.Config,
	target *serviceTarget,
) (uint16, error) {
	forwarder, err := t.createPortForwarder(restConfig, target)
	if err != nil {
		return 0, err
	}

	return t.waitForPortForward(ctx, forwarder, target)
}

func (t *Tunnel) createPortForwarder(
	restConfig *rest.Config,
	target *serviceTarget,
) (*portforward.PortForwarder, error) {
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward",
		t.config.ProxyNamespace, target.podName)
	serverURL := restConfig.Host + path

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL: %w", err)
	}

	spdyDialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		http.MethodPost,
		parsedURL,
	)

	// Use port 0 to let the system assign a local port
	ports := []string{fmt.Sprintf("0:%d", target.targetPort)}
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
	target *serviceTarget,
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
		t.markClosedLocked()

		return 0, fmt.Errorf("port-forward failed: %w", err)
	case <-ctx.Done():
		t.markClosedLocked()

		return 0, fmt.Errorf("context cancelled while waiting for port-forward: %w", ctx.Err())
	}

	// Monitor port-forward health — marks tunnel as closed if port-forward exits unexpectedly.
	// This enables early detection of stale tunnels when the talos-cluster-proxy pod is evicted or rescheduled.
	go t.monitorPortForward(errChan, logger)

	forwardedPorts, err := forwarder.GetPorts()
	if err != nil {
		t.markClosedLocked()

		return 0, fmt.Errorf("failed to get forwarded ports: %w", err)
	}

	if len(forwardedPorts) == 0 {
		t.markClosedLocked()

		return 0, ErrNoForwardedPorts
	}

	localPort := forwardedPorts[0].Local

	logger.Info("Port-forward started",
		zap.String("cluster", t.clusterName),
		zap.String("pod", target.podName),
		zap.Uint16("localPort", localPort),
		zap.Int32("remotePort", target.targetPort))

	return localPort, nil
}
