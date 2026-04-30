package talosproxy_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	handshakeTestTimeout = 2 * time.Second
)

// readCONNECTRequest reads the CONNECT request headers from the given connection using
// a bufio.Reader. Safe to call from test goroutines: it returns errors instead of using
// testify require.
func readCONNECTRequest(conn net.Conn) (*http.Request, error) {
	err := conn.SetReadDeadline(time.Now().Add(handshakeTestTimeout))
	if err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}

	return req, nil
}

func TestEstablishConnectTunnel_Success(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()

	defer func() { _ = clientConn.Close() }()

	const target = "10.200.0.5:50000"

	type serverResult struct {
		req     *http.Request
		payload string
		err     error
	}

	resultChan := make(chan serverResult, 1)

	go func() {
		defer func() { _ = serverConn.Close() }()

		req, readErr := readCONNECTRequest(serverConn)
		if readErr != nil {
			resultChan <- serverResult{err: readErr}

			return
		}

		_, writeErr := io.WriteString(serverConn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if writeErr != nil {
			resultChan <- serverResult{req: req, err: writeErr}

			return
		}

		buf := make([]byte, 128)

		bytesRead, err := serverConn.Read(buf)
		if err != nil {
			resultChan <- serverResult{req: req, err: err}

			return
		}

		resultChan <- serverResult{req: req, payload: string(buf[:bytesRead])}
	}()

	err := talosproxy.EstablishConnectTunnel(clientConn, target)
	require.NoError(t, err)

	// After the handshake, bytes written on the client side should arrive at the server
	// without any extra framing applied.
	_, err = io.WriteString(clientConn, "raw-tunnel-payload")
	require.NoError(t, err)

	result := <-resultChan
	require.NoError(t, result.err)

	assert.Equal(t, http.MethodConnect, result.req.Method)
	assert.Equal(t, target, result.req.Host)
	assert.Equal(t, target, result.req.RequestURI)
	assert.Equal(t, "raw-tunnel-payload", result.payload)
}

func TestEstablishConnectTunnel_NonOKStatus(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()

	defer func() { _ = clientConn.Close() }()

	go func() {
		defer func() { _ = serverConn.Close() }()

		_, _ = readCONNECTRequest(serverConn)

		_, _ = io.WriteString(serverConn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
	}()

	err := talosproxy.EstablishConnectTunnel(clientConn, "10.200.0.5:50000")
	require.Error(t, err)
	assert.ErrorIs(t, err, talosproxy.ErrConnectRejected)
}

func TestEstablishConnectTunnel_MalformedStatusLine(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()

	defer func() { _ = clientConn.Close() }()

	go func() {
		defer func() { _ = serverConn.Close() }()

		_, _ = readCONNECTRequest(serverConn)

		_, _ = io.WriteString(serverConn, "GARBAGE\r\n\r\n")
	}()

	err := talosproxy.EstablishConnectTunnel(clientConn, "10.200.0.5:50000")
	require.Error(t, err)
	assert.ErrorIs(t, err, talosproxy.ErrConnectMalformedResponse)
}

func TestEstablishConnectTunnel_ResponseTooLarge(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()

	defer func() { _ = clientConn.Close() }()

	go func() {
		defer func() { _ = serverConn.Close() }()

		_, _ = readCONNECTRequest(serverConn)

		// Write more than the max line size without any CRLF terminator.
		oversized := strings.Repeat("A", 8192)
		_, _ = io.WriteString(serverConn, oversized)
	}()

	err := talosproxy.EstablishConnectTunnel(clientConn, "10.200.0.5:50000")
	require.Error(t, err)
	assert.ErrorIs(t, err, talosproxy.ErrConnectResponseTooLarge)
}
