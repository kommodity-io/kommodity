package helpers

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	clusterReachablePollInterval = 5 * time.Second
	healthzPath                  = "/healthz"
	apiServerTargetPort          = 6443
	capiClusterNameLabel         = "cluster.x-k8s.io/cluster-name"
	kubeconfigSecretDataKey      = "value"
	portForwardReadyTimeout      = 30 * time.Second
	healthzRequestTimeout        = 10 * time.Second
)

//nolint:gosec // suffix is part of the secret name pattern, not a credential.
const kubeconfigSecretSuffix = "-kubeconfig"

// WaitForClusterReachable verifies that a Kommodity-managed workload cluster is reachable
// using the kubeconfig stored in the <clusterName>-kubeconfig secret.
//
// The control plane endpoint in the kubeconfig is a placeholder that is not routable from
// this process, so the helper port-forwards directly to a Ready pod backing the control
// plane Service in the infra cluster and rewrites the server URL accordingly.
func WaitForClusterReachable(
	kommodityCfg *rest.Config,
	infraCfg *rest.Config,
	secretNamespace string,
	clusterName string,
	infraNamespace string,
	timeout time.Duration,
) error {
	log.Printf("Waiting for workload cluster %q to be reachable...", clusterName)

	clientConfig, err := loadWorkloadKubeconfig(kommodityCfg, secretNamespace, clusterName)
	if err != nil {
		return err
	}

	infraClient, err := kubernetes.NewForConfig(infraCfg)
	if err != nil {
		return fmt.Errorf("failed to create infra cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	podName, err := findControlPlanePod(ctx, infraClient, infraNamespace, clusterName)
	if err != nil {
		return err
	}

	stopChan := make(chan struct{})
	defer close(stopChan)

	localPort, err := startAPIServerPortForward(infraCfg, infraNamespace, podName, stopChan)
	if err != nil {
		return err
	}

	workloadCfg, err := buildWorkloadRESTConfig(clientConfig, localPort)
	if err != nil {
		return err
	}

	err = pollHealthz(ctx, workloadCfg)
	if err != nil {
		return err
	}

	log.Printf("Workload cluster %q is reachable", clusterName)

	return nil
}

func loadWorkloadKubeconfig(
	kommodityCfg *rest.Config,
	secretNamespace string,
	clusterName string,
) (clientcmd.ClientConfig, error) {
	kommodityClient, err := kubernetes.NewForConfig(kommodityCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kommodity client: %w", err)
	}

	secretName := clusterName + kubeconfigSecretSuffix

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()

	secret, err := kommodityClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %q: %w", secretName, err)
	}

	data, ok := secret.Data[kubeconfigSecretDataKey]
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("%w: secret %q missing key %q", errKubeconfigInvalid, secretName, kubeconfigSecretDataKey)
	}

	clientConfig, err := clientcmd.NewClientConfigFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse kubeconfig from secret %q: %w",
			errKubeconfigInvalid, secretName, err)
	}

	return clientConfig, nil
}

// findControlPlanePod returns the name of a Ready pod that backs the control plane Service
// for the given cluster. The Service is identified by the CAPI cluster-name label and the
// backing pod is found via the Service selector.
func findControlPlanePod(
	ctx context.Context,
	infraClient kubernetes.Interface,
	infraNamespace string,
	clusterName string,
) (string, error) {
	services, err := infraClient.CoreV1().Services(infraNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: capiClusterNameLabel + "=" + clusterName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list control plane Services: %w", err)
	}

	if len(services.Items) == 0 {
		return "", fmt.Errorf("%w: cluster %q in namespace %q",
			errControlPlaneSvcNotFound, clusterName, infraNamespace)
	}

	service := services.Items[0]
	if len(service.Spec.Selector) == 0 {
		return "", fmt.Errorf("%w: Service %q has no selector",
			errControlPlaneSvcNotFound, service.Name)
	}

	selector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: service.Spec.Selector})

	pods, err := infraClient.CoreV1().Pods(infraNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for Service %q: %w", service.Name, err)
	}

	for _, pod := range pods.Items {
		if isPodReady(&pod) {
			return pod.Name, nil
		}
	}

	return "", fmt.Errorf("%w: Service %q in namespace %q",
		errControlPlanePodNotReady, service.Name, infraNamespace)
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// startAPIServerPortForward forwards the given pod's API server port to a local ephemeral port
// and returns the chosen local port once the forwarder is ready. The stopChan is owned by the
// caller; closing it stops the forwarder.
func startAPIServerPortForward(
	infraCfg *rest.Config,
	namespace string,
	podName string,
	stopChan chan struct{},
) (uint16, error) {
	transport, upgrader, err := spdy.RoundTripperFor(infraCfg)
	if err != nil {
		return 0, fmt.Errorf("failed to create SPDY round tripper: %w", err)
	}

	pfPath := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName)

	serverURL, err := url.Parse(infraCfg.Host + pfPath)
	if err != nil {
		return 0, fmt.Errorf("failed to parse port-forward URL: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, serverURL)

	readyChan := make(chan struct{})
	ports := []string{fmt.Sprintf("0:%d", apiServerTargetPort)}

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	errChan := make(chan error, 1)

	go func() {
		errChan <- forwarder.ForwardPorts()
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		return 0, fmt.Errorf("port-forward exited before ready: %w", err)
	case <-time.After(portForwardReadyTimeout):
		return 0, fmt.Errorf("%w: %s", errPortForwardNotReady, podName)
	}

	forwardedPorts, err := forwarder.GetPorts()
	if err != nil {
		return 0, fmt.Errorf("failed to read forwarded ports: %w", err)
	}

	if len(forwardedPorts) == 0 {
		return 0, fmt.Errorf("%w: pod %s", errPortForwardNoPorts, podName)
	}

	return forwardedPorts[0].Local, nil
}

func buildWorkloadRESTConfig(clientConfig clientcmd.ClientConfig, localPort uint16) (*rest.Config, error) {
	cfg, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build REST config from workload kubeconfig: %w", err)
	}

	cfg.Host = "https://127.0.0.1:" + strconv.Itoa(int(localPort))
	cfg.Insecure = true
	cfg.CAData = nil
	cfg.CAFile = ""
	cfg.Timeout = healthzRequestTimeout

	return cfg, nil
}

// pollHealthz issues GET /healthz against the workload API server until a 200 response is
// received or the context is cancelled.
func pollHealthz(ctx context.Context, cfg *rest.Config) error {
	tlsConfig, err := rest.TLSConfigFor(cfg)
	if err != nil {
		return fmt.Errorf("failed to build TLS config: %w", err)
	}

	// Insecure is set on cfg; ensure transport reflects it even if TLSConfigFor returned nil.
	// Workload API uses placeholder hostname; auth via client cert.
	if tlsConfig == nil {
		tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // see comment above.
	}

	httpClient := &http.Client{
		Timeout: healthzRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	ticker := time.NewTicker(clusterReachablePollInterval)
	defer ticker.Stop()

	for {
		ok, status, attemptErr := probeHealthz(ctx, httpClient, cfg)
		if ok {
			return nil
		}

		if attemptErr != nil {
			log.Printf("Workload cluster not yet reachable: %v", attemptErr)
		} else {
			log.Printf("Workload cluster /healthz returned %d, retrying...", status)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("workload cluster /healthz never returned 200: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func probeHealthz(ctx context.Context, client *http.Client, cfg *rest.Config) (bool, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Host+healthzPath, nil)
	if err != nil {
		return false, 0, fmt.Errorf("failed to build healthz request: %w", err)
	}

	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err //nolint:wrapcheck // caller logs raw error.
	}

	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK, resp.StatusCode, nil
}
