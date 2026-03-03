package talosproxy_test

import (
	"testing"

	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicProxyFunc_SingleHost(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:50000")
	t.Setenv("NO_PROXY", "localhost,127.0.0.0/8")

	request := talosproxy.NewTestRequest("https", "10.200.16.5:50000")

	proxyURL, err := talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "127.0.0.1:50000", proxyURL.Host)
}

func TestDynamicProxyFunc_CommaSeparatedHosts(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:50000")
	t.Setenv("NO_PROXY", "localhost,127.0.0.0/8")

	request := talosproxy.NewTestRequest("https", "10.200.16.5:50000,10.200.16.11:50000")

	proxyURL, err := talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "127.0.0.1:50000", proxyURL.Host)
}

func TestDynamicProxyFunc_NoProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	request := talosproxy.NewTestRequest("https", "10.200.16.5:50000")

	proxyURL, err := talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	assert.Nil(t, proxyURL)
}

func TestDynamicProxyFunc_LocalhostBypassed(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:50000")
	t.Setenv("NO_PROXY", "localhost,127.0.0.0/8")

	request := talosproxy.NewTestRequest("https", "127.0.0.1:6443")

	proxyURL, err := talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	assert.Nil(t, proxyURL)
}

func TestDynamicProxyFunc_DynamicEnvReading(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	request := talosproxy.NewTestRequest("https", "10.200.16.5:50000")

	// No proxy configured
	proxyURL, err := talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	assert.Nil(t, proxyURL)

	// Set proxy env var — dynamicProxyFunc should pick it up
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:50000")

	proxyURL, err = talosproxy.DynamicProxyFuncForTest(request)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "127.0.0.1:50000", proxyURL.Host)
}
