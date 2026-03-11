package talosproxy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testReadHeaderTimeout = 5 * time.Second
)

func newTestHandler(t *testing.T) *talosproxy.ConnectHandler {
	t.Helper()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      15050,
	}

	registry := talosproxy.NewCIDRRegistry()
	logger := zap.NewNop()
	pool := talosproxy.NewTunnelPool(proxyConfig, nil, logger)

	return talosproxy.NewConnectHandler(registry, pool, logger)
}

// startTestProxyServer starts an HTTP server using the handler and returns the listener.
func startTestProxyServer(
	t *testing.T,
	handler *talosproxy.ConnectHandler,
) net.Listener {
	t.Helper()

	listenConfig := net.ListenConfig{}

	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: testReadHeaderTimeout,
	}

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(func() { _ = server.Close() })

	return listener
}

// sendCONNECT sends a CONNECT request to the proxy and returns the status code.
func sendCONNECT(
	t *testing.T,
	proxyAddr string,
	target string,
) int {
	t.Helper()

	dialer := net.Dialer{}

	conn, err := dialer.DialContext(context.Background(), "tcp", proxyAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode
}

func TestConnectHandler_NonConnectMethod(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	listener := startTestProxyServer(t, handler)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		fmt.Sprintf("http://%s/", listener.Addr().String()),
		nil,
	)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestConnectHandler_InvalidTarget(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	listener := startTestProxyServer(t, handler)

	statusCode := sendCONNECT(t, listener.Addr().String(), "invalid-no-port")
	assert.Equal(t, http.StatusBadRequest, statusCode)
}

func TestConnectHandler_InvalidIP(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	listener := startTestProxyServer(t, handler)

	statusCode := sendCONNECT(t, listener.Addr().String(), "not-an-ip:15050")
	assert.Equal(t, http.StatusBadRequest, statusCode)
}

func TestConnectHandler_PassthroughNoCIDRMatch(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	// Start a target TCP server that echoes data
	echoListenConfig := net.ListenConfig{}

	echoListener, err := echoListenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = echoListener.Close() }()

	go func() {
		for {
			conn, acceptErr := echoListener.Accept()
			if acceptErr != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	listener := startTestProxyServer(t, handler)

	statusCode := sendCONNECT(t, listener.Addr().String(), echoListener.Addr().String())
	assert.Equal(t, http.StatusOK, statusCode)
}

func TestConnectHandler_TunnelDialFailure(t *testing.T) {
	t.Parallel()

	proxyConfig := &config.TalosProxyConfig{
		Enabled:        true,
		ListenPort:     0,
		ProxyNamespace: "talos-cluster-proxy",
		ProxyLabel:     "app=talos-cluster-proxy",
		ProxyPort:      15050,
	}

	registry := talosproxy.NewCIDRRegistry()
	logger := zap.NewNop()
	pool := talosproxy.NewTunnelPool(proxyConfig, fake.NewClientBuilder().Build(), logger)

	handler := talosproxy.NewConnectHandler(registry, pool, logger)

	// Register a CIDR that matches 10.200.0.0/20
	_, cidr, err := net.ParseCIDR("10.200.0.0/20")
	require.NoError(t, err)

	registry.Register("test-cluster", "default", cidr)

	listener := startTestProxyServer(t, handler)

	// CONNECT to an IP in the registered CIDR — tunnel will fail since there's no real cluster
	statusCode := sendCONNECT(t, listener.Addr().String(), "10.200.0.5:50000")
	assert.Equal(t, http.StatusBadGateway, statusCode)
}
