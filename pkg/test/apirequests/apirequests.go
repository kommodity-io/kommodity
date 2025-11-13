// Package apirequests provides utilities for making API requests in tests.
package apirequests

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
)

// APIRequest represents an API request to be made in tests.
type APIRequest struct {
	TestEnvironment      helpers.TestEnvironment
	Endpoint             string
	Type                 string
	ExpectedStatusCode   int
	ExpectedResponseBody string
}

// RunRequest executes the API request and verifies the response.
func (ar APIRequest) RunRequest(t *testing.T) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), ar.Type,
		"http://"+net.JoinHostPort(ar.TestEnvironment.AppHost, ar.TestEnvironment.AppPort)+ar.Endpoint, nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() {
		_ = resp.Body.Close()
	}()

	require.Equal(t, ar.ExpectedStatusCode, resp.StatusCode, "unexpected HTTP status code")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.JSONEq(t, ar.ExpectedResponseBody, string(body), "unexpected API response body")
}
