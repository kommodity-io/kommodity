package talosproxy

import (
	"net"
	"net/http"
	"net/url"
)

// DynamicProxyFuncForTest exports dynamicProxyFunc for testing.
var DynamicProxyFuncForTest = dynamicProxyFunc //nolint:gochecknoglobals // test export

// NewTestRequest creates an http.Request for testing proxy functions.
func NewTestRequest(scheme string, host string) *http.Request {
	return &http.Request{
		URL: &url.URL{
			Scheme: scheme,
			Host:   host,
		},
	}
}

// NewTrackedConn creates a trackedConn for testing.
func NewTrackedConn(conn net.Conn, tunnel *Tunnel) net.Conn {
	return &trackedConn{Conn: conn, tunnel: tunnel}
}
