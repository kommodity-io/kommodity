package talosproxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	// connectMaxLineBytes caps each HTTP response line read from the talos-cluster-proxy pod.
	// Response lines are expected to be short (status line + minimal headers).
	connectMaxLineBytes = 4096
	// connectStatusLineParts is the expected number of parts when splitting an HTTP status line by spaces.
	connectStatusLineParts = 3
)

// EstablishConnectTunnel performs an HTTP CONNECT handshake with the talos-cluster-proxy pod.
// On success (HTTP 200), the underlying connection carries raw TCP bytes end-to-end to the
// target address; no further framing is applied.
func EstablishConnectTunnel(conn io.ReadWriter, target string) error {
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	_, err := io.WriteString(conn, request)
	if err != nil {
		return fmt.Errorf("failed to write CONNECT request: %w", err)
	}

	err = readConnectResponse(conn)
	if err != nil {
		return fmt.Errorf("failed to read CONNECT response: %w", err)
	}

	return nil
}

// readConnectResponse parses the HTTP status line and drains remaining headers using
// byte-at-a-time reads. This prevents buffering bytes that belong to the tunnelled payload
// after the header terminator.
func readConnectResponse(reader io.Reader) error {
	statusLine, err := readHeaderLine(reader)
	if err != nil {
		return fmt.Errorf("failed to read status line: %w", err)
	}

	parts := strings.SplitN(statusLine, " ", connectStatusLineParts)
	if len(parts) < connectStatusLineParts-1 {
		return fmt.Errorf("%w: %q", ErrConnectMalformedResponse, statusLine)
	}

	if parts[1] != strconv.Itoa(http.StatusOK) {
		return fmt.Errorf("%w: %s", ErrConnectRejected, statusLine)
	}

	for {
		line, err := readHeaderLine(reader)
		if err != nil {
			return fmt.Errorf("failed to read header line: %w", err)
		}

		if line == "" {
			return nil
		}
	}
}

func readHeaderLine(reader io.Reader) (string, error) {
	buf := make([]byte, 0, connectMaxLineBytes)

	var oneByte [1]byte

	for len(buf) < connectMaxLineBytes {
		_, err := io.ReadFull(reader, oneByte[:])
		if err != nil {
			return "", fmt.Errorf("read byte: %w", err)
		}

		buf = append(buf, oneByte[0])

		if bytes.HasSuffix(buf, []byte("\r\n")) {
			return string(buf[:len(buf)-2]), nil
		}
	}

	return "", ErrConnectResponseTooLarge
}
