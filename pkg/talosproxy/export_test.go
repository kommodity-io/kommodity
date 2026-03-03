package talosproxy

import (
	"net/http"
	"net/url"
)

// DynamicProxyFuncForTest exports dynamicProxyFunc for testing.
var DynamicProxyFuncForTest = dynamicProxyFunc //nolint:gochecknoglobals // test export

// ProxyForCommaSeparatedHostsForTest exports proxyForCommaSeparatedHosts for testing.
func ProxyForCommaSeparatedHostsForTest(
	reqURL *url.URL,
	proxyFunc func(*url.URL) (*url.URL, error),
) (*url.URL, error) {
	return proxyForCommaSeparatedHosts(reqURL, proxyFunc)
}

// NewTestRequest creates an http.Request for testing proxy functions.
func NewTestRequest(scheme string, host string) *http.Request {
	return &http.Request{
		URL: &url.URL{
			Scheme: scheme,
			Host:   host,
		},
	}
}
