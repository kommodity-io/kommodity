package talosproxy

import (
	"net/http"
	"net/url"
	"strings"
	_ "unsafe" // required for go:linkname

	"golang.org/x/net/http/httpproxy"
)

const (
	// splitLimit is used with strings.SplitN to extract the first element from
	// a comma-separated host list.
	splitLimit = 2
)

// grpcHTTPSProxyFromEnvironment is linked to the gRPC internal variable
// google.golang.org/grpc/internal/resolver/delegatingresolver.HTTPSProxyFromEnvironment.
//
// By default, gRPC sets this to http.ProxyFromEnvironment, which has two problems
// for our use case:
//   - It caches the environment variables via sync.Once, so setting HTTPS_PROXY
//     after any HTTP call in the process has no effect.
//   - It cannot parse comma-separated hosts used by the Talos roundrobin resolver
//     (e.g., "10.200.16.5:50000,10.200.16.11:50000"), causing proxy detection
//     to return nil and bypassing our HTTP CONNECT proxy entirely.
//
// We replace it with dynamicProxyFunc which reads env vars on every call and
// handles comma-separated hosts by checking each individual address.
//
//nolint:gochecknoglobals // go:linkname requires a package-level variable
//go:linkname grpcHTTPSProxyFromEnvironment google.golang.org/grpc/internal/resolver/delegatingresolver.HTTPSProxyFromEnvironment
var grpcHTTPSProxyFromEnvironment func(*http.Request) (*url.URL, error)

//nolint:gochecknoinits // init is required to override the gRPC proxy function at startup
func init() {
	grpcHTTPSProxyFromEnvironment = dynamicProxyFunc
}

// dynamicProxyFunc reads HTTPS_PROXY and NO_PROXY from the environment on every
// call (unlike http.ProxyFromEnvironment which caches). It also handles
// comma-separated host:port strings produced by gRPC's roundrobin resolver by
// extracting the first address and checking it individually.
func dynamicProxyFunc(request *http.Request) (*url.URL, error) {
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()

	result, err := proxyFunc(request.URL)
	if result != nil || err != nil {
		return result, err
	}

	return proxyForCommaSeparatedHosts(request.URL, proxyFunc)
}

// proxyForCommaSeparatedHosts handles the case where the URL host is a
// comma-separated list of addresses (produced by gRPC's roundrobin resolver).
// net.SplitHostPort fails on such strings, causing the standard proxy lookup
// to return nil. We extract the first address and check it individually.
func proxyForCommaSeparatedHosts(
	reqURL *url.URL,
	proxyFunc func(*url.URL) (*url.URL, error),
) (*url.URL, error) {
	host := reqURL.Host
	if !strings.Contains(host, ",") {
		return nil, nil //nolint:nilnil // nil,nil means "no proxy" per http.ProxyFromEnvironment contract
	}

	firstAddr := strings.SplitN(host, ",", splitLimit)[0]

	testURL := &url.URL{
		Scheme: reqURL.Scheme,
		Host:   firstAddr,
	}

	return proxyFunc(testURL)
}
